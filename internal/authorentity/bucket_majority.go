package authorentity

import (
	"strings"
	"unicode/utf8"
)

// The bucket majority (E4j). D3 and the bucket decisions move a short
// name's book only on its own evidence, so a bucket whose books mostly went
// to one full name keeps the rest as an entity of the same name: «Стивен
// Кинг», 57 stories in anthologies beside the 768 books of the full name.
// After the decisions, a bucket whose moved books all went to the one full
// name of its short name — at least bucketMajorityMin of them and no fewer
// than stay, or at least bucketMajorityLarge and a third of the bucket
// (E4n) — gives that full name the rest, with the other short keys a
// decision joined to the bucket. A short name of several full names is a
// namesake's and keeps its bucket, unless the others are only
// spellings of the dominant one or negligible beside it; so does a book D3 keeps for
// itself: samizdat, a book the full name stands on, a vetoed one. It runs
// in the decision step only: the cards the candidates were sealed with, at
// level 0, stay as they were, and so do the decisions on them.

// bucketMajorityMin is the fewest moved books a majority needs.
const bucketMajorityMin = 3

// bucketMajorityLarge is the fewest moved books that are a majority at a
// third of the bucket (E4n): «Ольга Романовская», 32 of 77 books moved to
// «Ольга Викторовна Романовская». Below it a third is no evidence: «Мария
// Быкова», 6 fantasy novels moved beside 6 books of charms, is two women.
const bucketMajorityLarge = 10

// evidenceMajority names a book the majority moved: "D3:majority".
const evidenceMajority = "majority"

// The full names of one person (E4j round 2). A short name's full names in
// several entities are no namesakes when the others are the dominant one's
// spellings: the same full name, an additional part a typo away («Роберт
// Ирвин Говард», «Эрвин», «Ирвинг») or its initials («Василий В. Головачев»).
// Any other full name, however small, is a namesake's: a one-book «Ольга
// Петровна Пашнина» beside 54 books of «Олеговна» is the person whose short
// name the bucket's «Каменный пояс, 1975» carries.

// moveBucketRests gives each bucket's rest to its one full name when the
// bucket's moved books are a majority of it.
func (b *builder) moveBucketRests() {
	byBase := b.fullNamesByBase()
	for _, g := range b.keys {
		if !b.isBucketKey(g.idx) {
			continue
		}
		if root, alike, ok := b.onePerson(byBase[g.key.base()]); ok {
			b.moveBucketRest(g, root, alike)
		}
	}
}

// onePerson is the root of the dominant full name of full and the roots
// that are the same person, its own and its spellings'; ok is false when
// there is none or another person is among them.
func (b *builder) onePerson(full []int) (root int, alike map[int]bool, ok bool) {
	byRoot := map[int][]int{}
	for _, f := range full {
		r := b.find(f)
		byRoot[r] = append(byRoot[r], f)
	}
	if len(byRoot) == 0 {
		return 0, nil, false
	}
	books := map[int]int{}
	root = -1
	for _, r := range sortedKeys(byRoot) {
		books[r] = b.rootBooks(r)
		if root < 0 || books[r] > books[root] {
			root = r
		}
	}
	alike = map[int]bool{root: true}
	for r, keys := range byRoot {
		if r != root && !b.spellingOf(keys, byRoot[root]) {
			return 0, nil, false
		}
		alike[r] = true
	}
	return root, alike, true
}

// spellingOf reports full names of keys a spelling of the dominant's: the
// same additional part, one a typo away, or its initials; or a misparse
// whose additional part repeats its given or family name («Леонид Леонид
// Кудрявцев», «Леонид Ашкинази Ашкинази», E4n).
func (b *builder) spellingOf(keys, dominant []int) bool {
	for _, k := range keys {
		key := &b.keys[k].key
		if key.additional == key.given || key.additional == key.family {
			return true
		}
		for _, d := range dominant {
			a, c := key.additional, b.keys[d].key.additional
			if _, typo := typoCandidates(a, c); a == c || typo || initialsOf(a, c) {
				return true
			}
		}
	}
	return false
}

// initialsOf reports an additional part of initials, each the start of the
// other's word in its place («дж» of «джордж», «в» of «васильевич»).
func initialsOf(initials, full string) bool {
	iw, fw := strings.Fields(initials), strings.Fields(full)
	if len(iw) == 0 || len(iw) != len(fw) {
		return false
	}
	for i, w := range iw {
		if utf8.RuneCountInString(w) > maxInitialRunes || !strings.HasPrefix(fw[i], w) || w == fw[i] {
			return false
		}
	}
	return true
}

// maxInitialRunes is the longest initial: «Дж.», «Ст.».
const maxInitialRunes = 2

// rootBooks counts the books of the keys of a component.
func (b *builder) rootBooks(root int) int {
	books := map[int64]bool{}
	for _, k := range b.membersOf(root) {
		for _, id := range b.keys[k].books {
			books[id] = true
		}
	}
	return len(books)
}

// moveBucketRest moves the rest of bucket g to the full name root when its
// books moved to root or its spellings (alike) are the majority; a book
// moved to a negligible full name counts neither way. The bucket is g's
// short keys in its component: a short key a decision joined to it is the
// same person's and follows it, its books counted with it.
func (b *builder) moveBucketRest(g *keyGroup, root int, alike map[int]bool) {
	moved, stay := map[int64]bool{}, map[int64]bool{}
	var rest []int
	for _, k := range b.membersOf(b.find(g.idx)) {
		if b.keys[k].key.additional != "" {
			continue
		}
		for _, ci := range b.keys[k].credits {
			book := b.in.Credits[ci].BookID
			if to, ok := b.movedTo[ci]; ok {
				if alike[b.find(to)] {
					moved[book] = true
				}
				continue
			}
			stay[book] = true
			rest = append(rest, ci)
		}
	}
	if !majority(len(moved), len(stay)) {
		return
	}
	n := 0
	for _, ci := range rest {
		c := &b.in.Credits[ci]
		if isSamizdat(b.in.Books[c.BookID].Title) || b.rootOnBook(root, c.BookID) || b.bookVetoedTo(ci, root) {
			continue
		}
		b.movedTo[ci], b.movedRule[ci] = root, ruleD3+evidenceMajority
		n++
	}
	if n > 0 {
		b.rep.Buckets.MajorityBuckets++
		b.rep.Buckets.MajorityBooks += n
	}
}

// majority reports moved books a majority of a bucket of stay more: at
// least bucketMajorityMin and no fewer than stay, or at least
// bucketMajorityLarge and half of stay.
func majority(moved, stay int) bool {
	return moved >= bucketMajorityMin && moved >= stay || moved >= bucketMajorityLarge && 2*moved >= stay
}
