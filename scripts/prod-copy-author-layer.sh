#!/usr/bin/env bash
#
# Copy the production database into a local postgres container, for checks
# that need the whole author layer as it is on prod (entity builds, triage
# apply/undo) without touching prod.
#
# Read-only against production:
#   - every prod session runs with default_transaction_read_only=on;
#   - all reads happen inside one exported REPEATABLE READ snapshot, so every
#     part of every table, and the counts, see the same moment of a live prod;
#   - data leaves the pod as COPY ... TO STDOUT, gzipped inside the pod (an
#     uncompressed kubectl exec stream drops mid-table on the big tables);
#     a table over SPLIT_MIN_BYTES goes in PARTS parts, by id % PARTS, or by
#     block ranges when it has no integer id; every part is checked with
#     gzip -t and retried;
#   - favorite_books and invites are not copied; auth_user leaves prod reduced
#     to id plus synthetic username, email and password, every other column at
#     its local default; the Telegram secrets (bot tokens, webhook secrets,
#     link token hashes, raw updates, users' Telegram names) are replaced by
#     synthetic values in the SELECT, so they never leave the database.
#
# The schema comes from cmd/migrate of the release prod runs (the deployment's
# image tag by default), built from a temporary git worktree. The load runs
# with session_replication_role = replica (no triggers, no FK checks). Every
# table is truncated once, before any load; nothing truncates after a load,
# since TRUNCATE CASCADE of a later table would wipe earlier ones. The run
# fails if any table's count differs from prod's under the same snapshot.
#
# Usage: scripts/prod-copy-author-layer.sh
# Environment (all optional):
#   NAMESPACE, POD, PROD_DB      the prod database pod (cnpg-database, booksdump-pg-cluster-1, booksdump)
#   APP_NAMESPACE, APP_DEPLOY    where the prod release's tag is read (booksdump, gopds-api)
#   PROD_REF                     git ref whose cmd/migrate builds the schema (default: the deployed tag)
#   CONTAINER, PG_IMAGE, PG_PORT local container (triage-copy-pg, postgres:15.18-alpine, 55490)
#   COPY_DB                      local database name (booksdump_copy)
#   PARTS                        parts per split table (8)
#   SPLIT_MIN_BYTES              tables smaller than this go whole (64 MiB)
#   WORK_DIR                     where the parts and the reports go (.dumps/prod-copy, gitignored)
#   REPLACE=1                    drop an existing local COPY_DB first (otherwise the run refuses)
#   KEEP_PARTS=1                 keep the downloaded parts after a successful load

set -euo pipefail

NAMESPACE="${NAMESPACE:-cnpg-database}"
POD="${POD:-booksdump-pg-cluster-1}"
PROD_DB="${PROD_DB:-booksdump}"
APP_NAMESPACE="${APP_NAMESPACE:-booksdump}"
APP_DEPLOY="${APP_DEPLOY:-gopds-api}"
PROD_REF="${PROD_REF:-}"
CONTAINER="${CONTAINER:-triage-copy-pg}"
PG_IMAGE="${PG_IMAGE:-postgres:15.18-alpine}"
PG_PORT="${PG_PORT:-55490}"
COPY_DB="${COPY_DB:-booksdump_copy}"
COPY_USER="${COPY_USER:-gopds}"
COPY_PASS="${COPY_PASS:-gopds_password}"
PARTS="${PARTS:-8}"
SPLIT_MIN_BYTES="${SPLIT_MIN_BYTES:-67108864}"
WORK_DIR="${WORK_DIR:-.dumps/prod-copy}"
REPLACE="${REPLACE:-0}"
KEEP_PARTS="${KEEP_PARTS:-0}"
RETRIES=3

# Never copied. schema_migrations is written by cmd/migrate and compared, not copied.
EXCLUDED_TABLES=(favorite_books invites)

log() { printf '==> [%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die() { printf 'FATAL: %s\n' "$*" >&2; exit 1; }

# One read-only prod session per call.
prod_psql() {
    kubectl -n "$NAMESPACE" exec -i "$POD" -c postgres -- \
        env PGOPTIONS='-c default_transaction_read_only=on' \
        psql -U postgres -d "$PROD_DB" -XqAt -v ON_ERROR_STOP=1 "$@"
}

# A read-only prod session inside the held snapshot.
prod_in_snapshot() {
    prod_psql -c "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY" \
        -c "SET TRANSACTION SNAPSHOT '$SNAPSHOT'" "$@" -c "COMMIT"
}

# The local container listens on PG_PORT, and its socket follows the port.
local_psql() {
    docker exec -i -e PGPORT="$PG_PORT" "$CONTAINER" psql -U postgres -d "$COPY_DB" -XqAt -v ON_ERROR_STOP=1 "$@"
}

local_admin_psql() {
    docker exec -i -e PGPORT="$PG_PORT" "$CONTAINER" psql -U postgres -d postgres -XqAt -v ON_ERROR_STOP=1 "$@"
}

# The column list a table is copied with, and the SELECT list prod answers it
# with. Sensitive columns are replaced here, on the prod side.
select_expr() {
    local table="$1" column="$2"
    case "$table.$column" in
        auth_user.username) echo "'copy_user_' || id" ;;
        auth_user.email) echo "'copy_user_' || id || '@copy.invalid'" ;;
        auth_user.password) echo "'!'" ;;
        auth_user.date_joined) echo "to_timestamp(0)" ;;
        telegram_bot.token_enc) echo "CASE WHEN token_enc IS NULL THEN NULL ELSE decode(md5('copy-token-' || id), 'hex') END" ;;
        telegram_bot.token_sha256) echo "CASE WHEN token_sha256 IS NULL THEN NULL ELSE sha256(convert_to('copy-token-' || id, 'UTF8')) END" ;;
        telegram_bot.webhook_secret_enc) echo "CASE WHEN webhook_secret_enc IS NULL THEN NULL ELSE decode(md5('copy-webhook-' || id), 'hex') END" ;;
        telegram_bot.nav_key_enc) echo "CASE WHEN nav_key_enc IS NULL THEN NULL ELSE decode(md5('copy-nav-' || id), 'hex') END" ;;
        telegram_bot.webhook_path) echo "CASE WHEN webhook_path IS NULL THEN NULL ELSE md5('copy-path-' || id)::uuid END" ;;
        telegram_link_token.secret_sha256) echo "CASE WHEN secret_sha256 IS NULL THEN NULL ELSE sha256(convert_to('copy-link-' || id, 'UTF8')) END" ;;
        telegram_update_inbox.payload) echo "CASE WHEN payload IS NULL THEN NULL ELSE '{}'::jsonb END" ;;
        telegram_binding.tg_display) echo "CASE WHEN tg_display IS NULL THEN NULL ELSE 'copy_tg_' || id END" ;;
        *) echo "\"$column\"" ;;
    esac
}

# auth_user leaves prod with these columns only (date_joined is NOT NULL
# without a default); every other column takes its local default.
AUTH_USER_COLUMNS="id,username,email,password,date_joined"

# ---------------------------------------------------------------------------

command -v kubectl >/dev/null || die "kubectl is required"
command -v docker >/dev/null || die "docker is required"
command -v go >/dev/null || die "go is required"
mkdir -p "$WORK_DIR"
WORK_DIR="$(cd "$WORK_DIR" && pwd)"
PARTS_DIR="$WORK_DIR/parts"
REPO_ROOT="$(git rev-parse --show-toplevel)"

log "Checking prod: $NAMESPACE/$POD, database $PROD_DB"
[ "$(prod_psql -c 'SHOW default_transaction_read_only')" = "on" ] || die "prod session is not read-only"
prod_major="$(prod_psql -c 'SHOW server_version_num' | cut -c1-2)"
local_major="$(printf '%s' "$PG_IMAGE" | sed -E 's/^[^:]+:([0-9]+).*/\1/')"
[ "$prod_major" = "$local_major" ] || die "prod runs postgres $prod_major, PG_IMAGE is $PG_IMAGE"

if [ -z "$PROD_REF" ]; then
    image="$(kubectl -n "$APP_NAMESPACE" get deploy "$APP_DEPLOY" -o jsonpath='{.spec.template.spec.containers[0].image}')"
    PROD_REF="${image##*:}"
    log "Prod release (from $APP_DEPLOY): $PROD_REF"
fi
git -C "$REPO_ROOT" rev-parse --verify --quiet "$PROD_REF^{commit}" >/dev/null \
    || die "ref $PROD_REF is not in this repository (git fetch --tags?)"

# --- Local container ---------------------------------------------------------

if ! docker inspect "$CONTAINER" >/dev/null 2>&1; then
    log "Starting $CONTAINER ($PG_IMAGE) on the host network, port $PG_PORT"
    # A disposable copy: durability is traded for load speed.
    docker run -d --name "$CONTAINER" --network host \
        -e POSTGRES_PASSWORD="$COPY_PASS" \
        "$PG_IMAGE" \
        -c port="$PG_PORT" -c listen_addresses=127.0.0.1 \
        -c fsync=off -c synchronous_commit=off -c full_page_writes=off \
        -c wal_level=minimal -c max_wal_senders=0 -c max_wal_size=8GB \
        -c checkpoint_timeout=30min -c maintenance_work_mem=512MB \
        -c shared_buffers=1GB -c work_mem=64MB >/dev/null
elif [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER")" != "true" ]; then
    log "Starting the existing $CONTAINER"
    docker start "$CONTAINER" >/dev/null
fi
for _ in $(seq 1 60); do
    docker exec "$CONTAINER" pg_isready -U postgres -p "$PG_PORT" -q 2>/dev/null && break
    sleep 1
done
docker exec "$CONTAINER" pg_isready -U postgres -p "$PG_PORT" -q || die "$CONTAINER is not ready"

if [ "$(local_admin_psql -c "SELECT count(*) FROM pg_database WHERE datname = '$COPY_DB'")" != "0" ]; then
    [ "$REPLACE" = "1" ] || die "database $COPY_DB already exists in $CONTAINER (REPLACE=1 drops it)"
    log "Dropping the existing $COPY_DB"
    local_admin_psql -c "DROP DATABASE \"$COPY_DB\" WITH (FORCE)"
fi
if [ "$(local_admin_psql -c "SELECT count(*) FROM pg_roles WHERE rolname = '$COPY_USER'")" = "0" ]; then
    local_admin_psql -c "CREATE ROLE \"$COPY_USER\" LOGIN SUPERUSER PASSWORD '$COPY_PASS'"
fi
local_admin_psql -c "CREATE DATABASE \"$COPY_DB\" OWNER \"$COPY_USER\""

# --- Schema from the prod release -------------------------------------------

log "Building cmd/migrate at $PROD_REF"
tree="$(mktemp -d)"
trap 'git -C "$REPO_ROOT" worktree remove --force "$tree" >/dev/null 2>&1 || true' EXIT
git -C "$REPO_ROOT" worktree add --detach --quiet "$tree" "$PROD_REF"
(cd "$tree" && GOMAXPROCS=2 go build -o "$WORK_DIR/migrate" ./cmd/migrate)
git -C "$REPO_ROOT" worktree remove --force "$tree"
trap - EXIT

log "Migrating $COPY_DB"
"$WORK_DIR/migrate" -host "127.0.0.1:$PG_PORT" -user "$COPY_USER" -password "$COPY_PASS" -database "$COPY_DB" \
    > "$WORK_DIR/migrate.log"

prod_migrations="$(prod_psql -c 'SELECT version FROM schema_migrations ORDER BY 1')"
local_migrations="$(local_psql -c 'SELECT version FROM schema_migrations ORDER BY 1')"
if [ "$prod_migrations" != "$local_migrations" ]; then
    diff <(printf '%s\n' "$prod_migrations") <(printf '%s\n' "$local_migrations") >&2 || true
    die "the migrations of $PROD_REF differ from prod's schema_migrations (< prod, > local)"
fi

# --- Tables and columns -----------------------------------------------------

tables_sql="SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition ORDER BY 1"
prod_tables="$(prod_psql -c "$tables_sql")"
local_tables="$(local_psql -c "$tables_sql")"
if [ "$prod_tables" != "$local_tables" ]; then
    diff <(printf '%s\n' "$prod_tables") <(printf '%s\n' "$local_tables") >&2 || true
    die "prod and local tables differ (< prod, > local)"
fi

# Non-generated columns, in local order, and whether an integer id exists.
columns_sql="SELECT c.relname, string_agg(quote_ident(a.attname), ',' ORDER BY a.attnum)
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
    WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') GROUP BY 1 ORDER BY 1"
prod_columns="$(prod_psql -F $'\t' -c "$(printf '%s' "$columns_sql" | sed 's/ORDER BY a.attnum/ORDER BY a.attname/')")"
local_columns_sorted="$(local_psql -F $'\t' -c "$(printf '%s' "$columns_sql" | sed 's/ORDER BY a.attnum/ORDER BY a.attname/')")"
if [ "$prod_columns" != "$local_columns_sorted" ]; then
    diff <(printf '%s\n' "$prod_columns" | tr ',' '\n') <(printf '%s\n' "$local_columns_sorted" | tr ',' '\n') >&2 || true
    die "prod and local columns differ (< prod, > local)"
fi
declare -A COLUMNS
while IFS=$'\t' read -r table cols; do
    COLUMNS["$table"]="$cols"
done < <(local_psql -F $'\t' -c "$columns_sql")

declare -A INT_ID
while read -r table; do
    INT_ID["$table"]=1
done < <(local_psql -c "SELECT table_name FROM information_schema.columns
    WHERE table_schema = 'public' AND column_name = 'id' AND data_type IN ('smallint', 'integer', 'bigint')")

COPY_TABLES=()
while read -r table; do
    skip=0
    for excluded in "${EXCLUDED_TABLES[@]}" schema_migrations; do
        [ "$table" = "$excluded" ] && skip=1
    done
    [ "$skip" = "1" ] || COPY_TABLES+=("$table")
done <<< "$local_tables"
log "${#COPY_TABLES[@]} tables to copy (left out: ${EXCLUDED_TABLES[*]}, schema_migrations)"

# --- The held snapshot -----------------------------------------------------

log "Opening the read-only snapshot on prod"
coproc SNAP {
    kubectl -n "$NAMESPACE" exec -i "$POD" -c postgres -- \
        env PGOPTIONS='-c default_transaction_read_only=on' \
        psql -U postgres -d "$PROD_DB" -XqAt -v ON_ERROR_STOP=1 2>&1
}
SNAP_IN="${SNAP[1]}"
SNAP_OUT="${SNAP[0]}"
close_snapshot() {
    if [ -n "${SNAP_IN:-}" ]; then
        echo "COMMIT;" >&"$SNAP_IN" 2>/dev/null || true
        # Grouped: a bare exec with 2>/dev/null would silence the rest of the script.
        { exec {SNAP_IN}>&-; } 2>/dev/null || true
        SNAP_IN=""
    fi
}
trap close_snapshot EXIT
echo "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();" >&"$SNAP_IN"
read -r -t 60 SNAPSHOT <&"$SNAP_OUT" || die "no snapshot from prod"
[[ "$SNAPSHOT" =~ ^[0-9A-F-]+$ ]] || die "unexpected snapshot answer: $SNAPSHOT"
log "Snapshot $SNAPSHOT"
started_at="$(date +%s)"

# Table sizes (bytes) decide whole vs split.
declare -A SIZE
while IFS=$'\t' read -r table bytes; do
    SIZE["$table"]="$bytes"
done < <(prod_psql -F $'\t' -c "SELECT c.relname, pg_relation_size(c.oid) FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'r'")

# --- Download ---------------------------------------------------------------

rm -rf "$PARTS_DIR"
mkdir -p "$PARTS_DIR"

# part_filters prints one WHERE clause per part of a table.
part_filters() {
    local table="$1" size="${SIZE[$1]:-0}"
    if [ "$size" -lt "$SPLIT_MIN_BYTES" ]; then
        echo "true"
    elif [ -n "${INT_ID[$table]:-}" ]; then
        for k in $(seq 0 $((PARTS - 1))); do echo "id % $PARTS = $k"; done
    else
        # No integer id: split by block ranges (TID range scans). The first
        # range has no lower bound and the last no upper one, so a table that
        # grew since its size was read is still covered whole.
        local blocks=$((size / 8192)) k lo hi
        local step=$(((blocks + PARTS - 1) / PARTS))
        for k in $(seq 0 $((PARTS - 1))); do
            lo=$((k * step)); hi=$(((k + 1) * step))
            if [ "$k" = 0 ]; then echo "ctid < '($hi,0)'"
            elif [ "$k" = $((PARTS - 1)) ]; then echo "ctid >= '($lo,0)'"
            else echo "ctid >= '($lo,0)' AND ctid < '($hi,0)'"; fi
        done
    fi
}

select_list() {
    local table="$1" cols list="" col
    if [ "$table" = "auth_user" ]; then cols="$AUTH_USER_COLUMNS"; else cols="${COLUMNS[$table]}"; fi
    IFS=',' read -ra arr <<< "$cols"
    for col in "${arr[@]}"; do
        col="${col//\"/}"
        list+="${list:+, }$(select_expr "$table" "$col")"
    done
    printf '%s' "$list"
}

load_columns() {
    if [ "$1" = "auth_user" ]; then printf '%s' "$AUTH_USER_COLUMNS"; else printf '%s' "${COLUMNS[$1]}"; fi
}

download_part() {
    local table="$1" filter="$2" out="$3" query attempt
    query="COPY (SELECT $(select_list "$table") FROM public.\"$table\" WHERE $filter) TO STDOUT"
    for attempt in $(seq 1 "$RETRIES"); do
        # The snapshot statements and COPY go to psql on stdin, so the query
        # text never meets a shell inside the pod.
        if printf '%s\n' \
            "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;" \
            "SET TRANSACTION SNAPSHOT '$SNAPSHOT';" \
            "$query;" \
            "COMMIT;" \
            | kubectl -n "$NAMESPACE" exec -i "$POD" -c postgres -- \
                bash -o pipefail -c "PGOPTIONS='-c default_transaction_read_only=on' psql -U postgres -d '$PROD_DB' -XqAt -v ON_ERROR_STOP=1 | gzip -1" \
                > "$out.tmp" \
            && gzip -t "$out.tmp" 2>/dev/null; then
            mv "$out.tmp" "$out"
            return 0
        fi
        log "  retry $attempt for $table ($filter)"
        sleep 2
    done
    die "could not download $table ($filter)"
}

log "Downloading"
declare -A PART_FILES
for table in "${COPY_TABLES[@]}"; do
    k=0
    files=()
    while read -r filter; do
        out="$PARTS_DIR/$table.$k.gz"
        download_part "$table" "$filter" "$out"
        files+=("$out")
        k=$((k + 1))
    done < <(part_filters "$table")
    PART_FILES["$table"]="${files[*]}"
    log "  $table: $k part(s), $(du -ch "${files[@]}" | tail -1 | cut -f1)"
done
download_done="$(date +%s)"

# Prod counts under the same snapshot, and the prod sequence values.
log "Counting prod rows in the snapshot"
count_sql="SELECT string_agg(format('SELECT %L, count(*) FROM public.%I', t, t), ' UNION ALL ' ORDER BY t)
    FROM unnest(ARRAY['$(IFS=,; echo "${COPY_TABLES[*]}" | sed "s/,/','/g")']) AS t"
count_query="$(prod_psql -c "$count_sql")"
prod_in_snapshot -F $'\t' -c "$count_query" | sort > "$WORK_DIR/prod-counts.tsv"
prod_psql -F $'\t' -c "SELECT sequencename, last_value FROM pg_sequences
    WHERE schemaname = 'public' AND last_value IS NOT NULL ORDER BY 1" > "$WORK_DIR/prod-sequences.tsv"
close_snapshot
trap - EXIT
log "Snapshot closed"

# --- Load -------------------------------------------------------------------

log "Truncating every local table once, before the load"
truncate_list="$(printf 'public.%s, ' "${COPY_TABLES[@]}" | sed 's/, $//')"
local_psql -c "TRUNCATE $truncate_list RESTART IDENTITY CASCADE"

log "Loading"
for table in "${COPY_TABLES[@]}"; do
    for file in ${PART_FILES[$table]}; do
        gzip -dc "$file" | local_psql \
            -c "SET session_replication_role = replica" \
            -c "COPY public.\"$table\" ($(load_columns "$table")) FROM STDIN" >/dev/null
    done
done

log "Setting sequences to prod's values"
# One batch on stdin: a docker exec -i inside a while-read loop would eat the
# rest of the file after the first sequence.
awk -F'\t' '{ printf "SELECT setval(%s, %s);\n", "'"'"'public.\"" $1 "\"'"'"'", $2 }' \
    "$WORK_DIR/prod-sequences.tsv" | local_psql >/dev/null
local_psql -F $'\t' -c "SELECT sequencename, last_value FROM pg_sequences
    WHERE schemaname = 'public' AND last_value IS NOT NULL ORDER BY 1" > "$WORK_DIR/local-sequences.tsv"
if ! diff "$WORK_DIR/prod-sequences.tsv" "$WORK_DIR/local-sequences.tsv" >&2; then
    die "sequences differ from prod (< prod, > copy)"
fi

log "Analyzing"
local_psql -c "ANALYZE" >/dev/null
load_done="$(date +%s)"

# --- Compare -----------------------------------------------------------------

local_count_query="$(local_psql -c "$count_sql")"
local_psql -F $'\t' -c "$local_count_query" | sort > "$WORK_DIR/local-counts.tsv"
join -t $'\t' -a1 -a2 -e MISSING -o 0,1.2,2.2 "$WORK_DIR/prod-counts.tsv" "$WORK_DIR/local-counts.tsv" \
    | awk -F'\t' 'BEGIN { OFS = "\t"; print "table", "prod", "copy", "ok" } { print $1, $2, $3, ($2 == $3 ? "ok" : "DIFF") }' \
    > "$WORK_DIR/counts.tsv"
if grep -q 'DIFF$' "$WORK_DIR/counts.tsv"; then
    grep 'DIFF$' "$WORK_DIR/counts.tsv" >&2
    die "row counts differ from prod (see $WORK_DIR/counts.tsv)"
fi

{
    echo "prod_ref	$PROD_REF"
    echo "snapshot	$SNAPSHOT"
    echo "tables	${#COPY_TABLES[@]}"
    echo "parts_bytes	$(du -sb "$PARTS_DIR" | cut -f1)"
    echo "download_seconds	$((download_done - started_at))"
    echo "load_seconds	$((load_done - download_done))"
    echo "copy_db_bytes	$(local_psql -c "SELECT pg_database_size(current_database())")"
} > "$WORK_DIR/summary.tsv"

[ "$KEEP_PARTS" = "1" ] || rm -rf "$PARTS_DIR"
log "Done: every table matches prod ($WORK_DIR/counts.tsv, $WORK_DIR/summary.tsv)"
log "Copy: postgres://$COPY_USER:$COPY_PASS@127.0.0.1:$PG_PORT/$COPY_DB"
