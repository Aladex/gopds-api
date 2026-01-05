package parser

import "strings"

// TagHandler matches a tag path and collects text values and attributes.
type TagHandler struct {
	tags            []string
	pathSize        int
	currentIndex    int
	values          []string
	attributesList  []map[string]string
	currentAttrs    map[string]string
	processingValue bool
	currentValue    string
}

// NewTagHandler initializes a TagHandler for the provided tag path.
func NewTagHandler(tags []string) *TagHandler {
	return &TagHandler{
		tags:         tags,
		pathSize:     len(tags),
		currentIndex: -1,
	}
}

// Reset clears the internal state for reuse.
func (h *TagHandler) Reset() {
	h.currentIndex = -1
	h.values = nil
	h.attributesList = nil
	h.currentAttrs = nil
	h.processingValue = false
	h.currentValue = ""
}

// OpenTag updates state on an opening tag and returns true if the full path matched.
func (h *TagHandler) OpenTag(tag string, attrs map[string]string) bool {
	matched := false
	if h.currentIndex+1 < h.pathSize && h.tags[h.currentIndex+1] == tag {
		h.currentIndex++
	}
	if h.currentIndex+1 == h.pathSize {
		h.currentAttrs = copyAttrs(attrs)
		h.attributesList = append(h.attributesList, h.currentAttrs)
		matched = true
	}
	return matched
}

// CloseTag updates state on a closing tag and flushes any pending text.
func (h *TagHandler) CloseTag(tag string) {
	if h.currentIndex >= 0 && h.tags[h.currentIndex] == tag {
		h.currentIndex--
		if h.processingValue {
			h.values = append(h.values, strings.TrimSpace(h.currentValue))
			h.processingValue = false
			h.currentValue = ""
		}
	}
}

// SetValue appends text data when the handler is at the target depth.
func (h *TagHandler) SetValue(data string) {
	if h.currentIndex+1 == h.pathSize {
		if !h.processingValue {
			h.currentValue = data
			h.processingValue = true
		} else {
			h.currentValue += data
		}
	}
}

// GetValues returns collected text values.
func (h *TagHandler) GetValues() []string {
	return h.values
}

// GetText joins collected values using the divider.
func (h *TagHandler) GetText(divider string) string {
	return strings.Join(h.values, divider)
}

// GetAttribute returns the attribute value from the last matched tag.
func (h *TagHandler) GetAttribute(attr string) (string, bool) {
	if h.currentAttrs == nil {
		return "", false
	}
	val, ok := h.currentAttrs[attr]
	return val, ok
}

// GetAttributes returns attribute values from all matched tags.
func (h *TagHandler) GetAttributes(attr string) []string {
	var values []string
	for _, attrs := range h.attributesList {
		if val, ok := attrs[attr]; ok {
			values = append(values, val)
		}
	}
	return values
}

func copyAttrs(attrs map[string]string) map[string]string {
	if attrs == nil {
		return nil
	}
	cp := make(map[string]string, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	return cp
}
