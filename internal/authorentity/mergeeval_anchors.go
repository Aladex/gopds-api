package authorentity

import (
	"bytes"
	"crypto/sha256"
	"slices"
	"strconv"
)

// The known-same anchors of the bucket and translit shapes (E4c-2b, plan
// §3.2): what the build already holds as one person, asked as the merge
// question would ask it, measured as recall and never gated.
//
//   - bucket: a book D3 moved out of a short-name bucket, under the bucket
//     credit's name, against the card of the full name it moved to without
//     that book;
//   - translit: a Cyrillic and a Latin key group of one side (a Wikidata or
//     D2b merge), each as a card of its own names and books.

// MergeBucketAnchorPairs are the bucket anchors, in a fixed hash order: per
// short-name key group its first credit D3 moved to a full name (the
// bucket may be empty after it), when the full side is of the key's
// script, not tagged and not manual, and holds a book of its own.
func (p *Plan) MergeBucketAnchorPairs() []MergeAnchorPair {
	if p.cand == nil {
		return nil
	}
	b := p.cand.b
	var out []MergeAnchorPair
	for k := range b.keys {
		kg := b.keys[k]
		script := kg.key.script
		if script != ScriptCyrl && script != ScriptLatn {
			continue
		}
		for _, ci := range kg.credits {
			to, moved := b.movedTo[ci]
			if !moved || b.movedMethod[ci] != "" {
				continue
			}
			full, ok := p.cand.sides[b.find(to)]
			if !ok || full.tagged || full.manual || full.script != script || len(full.books) < 2 {
				continue
			}
			out = append(out, MergeAnchorPair{Kind: CandidateBucket, Script: script, Entity: full.entity,
				credit: ci, full: full.root})
			break
		}
	}
	slices.SortStableFunc(out, func(x, y MergeAnchorPair) int {
		return bytes.Compare(bucketAnchorOrder(&x, b), bucketAnchorOrder(&y, b))
	})
	return out
}

// bucketAnchorOrder is a bucket anchor's place in the anchors' order, and
// its item key.
func bucketAnchorOrder(a *MergeAnchorPair, b *builder) []byte {
	h := sha256.New()
	h.Write([]byte(AnchorVersion + "|" + CandidateBucket + "|" + strconv.FormatInt(b.in.Credits[a.credit].ID, 10) + "|"))
	h.Write(a.Entity[:])
	return h.Sum(nil)
}

// bucketAnchorBooks are a bucket anchor's card books: the moved book, then
// the full side's sample without it.
func (p *Plan) bucketAnchorBooks(a *MergeAnchorPair) []int64 {
	moved := p.cand.b.in.Credits[a.credit].BookID
	rest := p.bucketAnchorRest(a)
	return append([]int64{moved}, rest[:min(mergeCardSample, len(rest))]...)
}

// bucketAnchorRest is the full side's books without the moved one.
func (p *Plan) bucketAnchorRest(a *MergeAnchorPair) []int64 {
	moved := p.cand.b.in.Credits[a.credit].BookID
	full := p.cand.sides[a.full]
	rest := make([]int64, 0, len(full.books))
	for _, id := range full.books {
		if id != moved {
			rest = append(rest, id)
		}
	}
	return rest
}

// renderBucketAnchor is a bucket anchor's two cards: the moved book under
// its credit's name, and the full side without it.
func (p *Plan) renderBucketAnchor(r *cardRenderer, a *MergeAnchorPair) MergeInput {
	c := &p.cand.b.in.Credits[a.credit]
	full := p.cand.sides[a.full]
	book := MergeCardOf([]string{c.DisplayName}, []string{c.Nickname}, 1, []MergeCardBook{r.book(c.BookID, a.Entity)})
	return MergeInput{CardVersion: MergeCardVersion, A: book, B: r.cardOf(full.names, full.variants,
		p.bucketAnchorRest(a), full.entity)}
}

// MergeTranslitAnchorPairs are the translit anchors, in a fixed hash
// order: per side at most one pair of a Cyrillic and a Latin person key
// group with no book in common. Bucket, tagged and manual sides are never
// used.
func (p *Plan) MergeTranslitAnchorPairs() []MergeAnchorPair {
	if p.cand == nil {
		return nil
	}
	b := p.cand.b
	var out []MergeAnchorPair
	for _, root := range sortedKeys(p.cand.sides) {
		s := p.cand.sides[root]
		if s.bucket || s.tagged || s.manual || len(s.keys) < 2 {
			continue
		}
		if pair, ok := b.translitAnchorPair(s); ok {
			out = append(out, pair)
		}
	}
	slices.SortStableFunc(out, func(x, y MergeAnchorPair) int {
		return bytes.Compare(anchorOrder(&x, b), anchorOrder(&y, b))
	})
	return out
}

// translitAnchorPair is a side's first Cyrillic and Latin person key groups
// with credits and no book in common.
func (b *builder) translitAnchorPair(s *candSide) (MergeAnchorPair, bool) {
	keys := slices.Clone(s.keys)
	slices.Sort(keys)
	for _, x := range keys {
		kx := b.keys[x]
		if kx.key.script != ScriptCyrl || kx.key.kind != KindPerson || len(kx.credits) == 0 {
			continue
		}
		for _, y := range keys {
			ky := b.keys[y]
			if ky.key.script != ScriptLatn || ky.key.kind != KindPerson || len(ky.credits) == 0 ||
				overlaps(kx.books, ky.books) {
				continue
			}
			return MergeAnchorPair{Kind: CandidateTranslit, Script: ScriptCyrlLatn, Entity: s.entity, ka: x, kb: y}, true
		}
	}
	return MergeAnchorPair{}, false
}
