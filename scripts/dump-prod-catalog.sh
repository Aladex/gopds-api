#!/usr/bin/env bash
#
# Pull a development dataset out of the production database.
#
# Only the catalog is taken: books, authors, genres, series and collections.
# auth_user, favorite_books and invites are deliberately left behind — they hold
# real email addresses, password hashes and live Telegram bot tokens, and no test
# needs them. Use `make db-seed` afterwards to create synthetic users instead.
#
# Every operation here is read-only against production.

set -euo pipefail

NAMESPACE="${NAMESPACE:-cnpg-database}"
POD="${POD:-booksdump-pg-cluster-1}"
DB="${DB:-booksdump}"
OUT_DIR="${OUT_DIR:-.dumps}"

# Catalog tables, in dependency order. Keep this list explicit rather than using
# a wildcard, so adding a table that holds user data cannot silently join the
# dump.
CATALOG_TABLES=(
    opds_catalog_catalog
    opds_catalog_author
    opds_catalog_genre
    opds_catalog_series
    opds_catalog_book
    opds_catalog_bauthor
    opds_catalog_bgenre
    opds_catalog_bseries
    book_collections
    book_collection_items
    book_collection_books
    collection_votes
    book_match_decisions
    covers
)

# Tables that must never be copied out of production.
EXCLUDED_TABLES=(auth_user favorite_books invites)

log() { printf '==> %s\n' "$*" >&2; }

in_pod() {
    kubectl -n "$NAMESPACE" exec -i "$POD" -c postgres -- "$@"
}

mkdir -p "$OUT_DIR"

log "Checking connectivity to $POD in namespace $NAMESPACE"
in_pod psql -U postgres -d "$DB" -tAc 'SELECT 1' > /dev/null

log "Dumping schema"
in_pod pg_dump -U postgres -d "$DB" --schema-only --no-owner --no-privileges \
    | gzip > "$OUT_DIR/schema.sql.gz"

table_args=()
for table in "${CATALOG_TABLES[@]}"; do
    table_args+=(--table="$table")
done

log "Dumping catalog data (${#CATALOG_TABLES[@]} tables, excluding: ${EXCLUDED_TABLES[*]})"
in_pod pg_dump -U postgres -d "$DB" --data-only --no-owner --no-privileges \
    --disable-triggers "${table_args[@]}" \
    | gzip > "$OUT_DIR/catalog.sql.gz"

log "Done:"
ls -lh "$OUT_DIR"/*.sql.gz >&2

for table in "${EXCLUDED_TABLES[@]}"; do
    if zgrep -q "COPY public.$table " "$OUT_DIR/catalog.sql.gz"; then
        echo "FATAL: $table leaked into the dump" >&2
        exit 1
    fi
done
log "Verified: no excluded table is present in the dump"
