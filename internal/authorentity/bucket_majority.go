package authorentity

// The bucket majority (E4j). D3 and the bucket decisions move a short
// name's book only on its own evidence, so a bucket whose books mostly went
// to one full name keeps the rest as an entity of the same name: «Стивен
// Кинг», 57 stories in anthologies beside the 768 books of the full name.
// After the decisions, a bucket whose moved books all went to the one full
// name of its short name — at least bucketMajorityMin of them and no fewer
// than stay — gives that full name the rest. A short name of several full
// names is a namesake's and keeps its bucket; so does a book D3 keeps for
// itself: samizdat, a book the full name stands on, a vetoed one. It runs
// in the decision step only: the cards the candidates were sealed with, at
// level 0, stay as they were, and so do the decisions on them.

// bucketMajorityMin is the fewest moved books a majority needs.
const bucketMajorityMin = 3

// evidenceMajority names a book the majority moved: "D3:majority".
const evidenceMajority = "majority"

// moveBucketRests gives each bucket's rest to its one full name when the
// bucket's moved books are a majority of it.
func (b *builder) moveBucketRests() {
	byBase := b.fullNamesByBase()
	for _, g := range b.keys {
		if !b.isBucketKey(g.idx) {
			continue
		}
		roots := map[int]bool{}
		for _, f := range byBase[g.key.base()] {
			roots[b.find(f)] = true
		}
		if len(roots) != 1 {
			continue
		}
		for root := range roots {
			b.moveBucketRest(g, root)
		}
	}
}

// moveBucketRest moves the rest of bucket g to the full name root when its
// moved books are the majority. With one full name every move went there:
// D3 and the bucket decisions move a book only to a full name of its short
// name.
func (b *builder) moveBucketRest(g *keyGroup, root int) {
	moved, stay := map[int64]bool{}, map[int64]bool{}
	var rest []int
	for _, ci := range g.credits {
		book := b.in.Credits[ci].BookID
		if _, ok := b.movedTo[ci]; ok {
			moved[book] = true
			continue
		}
		stay[book] = true
		rest = append(rest, ci)
	}
	if len(moved) < bucketMajorityMin || len(moved) < len(stay) {
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
