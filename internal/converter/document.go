package converter

// document.go holds the shared semantic views and traversal of the parsed
// FB2 tree. Both the EPUB chapter builder and the preview chunker walk the
// document through these helpers, so they agree on what "the content" is.

// Paragraphs returns the section's own paragraphs in document order, skipping
// nested sections. It is a view over Content for callers that only care about
// text; callers that need the interleaved order of paragraphs and sections
// must read Content directly.
func (s *FB2BodySection) Paragraphs() []*FB2Paragraph {
	if s == nil {
		return nil
	}
	var out []*FB2Paragraph
	for _, item := range s.Content {
		if item != nil && item.Paragraph != nil {
			out = append(out, item.Paragraph)
		}
	}
	return out
}

// SubSections returns the section's nested sections in document order.
func (s *FB2BodySection) SubSections() []*FB2BodySection {
	if s == nil {
		return nil
	}
	var out []*FB2BodySection
	for _, item := range s.Content {
		if item != nil && item.Section != nil {
			out = append(out, item.Section)
		}
	}
	return out
}

// WalkSections visits every section in preorder (parent before children),
// passing its depth: top-level sections are depth 1.
func WalkSections(sections []*FB2BodySection, depth int, visit func(section *FB2BodySection, depth int)) {
	for _, section := range sections {
		if section == nil {
			continue
		}
		visit(section, depth)
		WalkSections(section.SubSections(), depth+1, visit)
	}
}
