package a2ui

import "strings"

// FencePreview removes A2UI fence spans from a live text preview, in order,
// across chunk boundaries.
//
// The fence is an encoding this platform asks the model for; it is not prose. A
// client that streamed deltas verbatim therefore showed raw surface JSON until
// item.completed replaced it, and every client had to strip markers it never
// asked about. Terminal text is untouched: extraction still sees the model's
// output whole (SplitTextAndA2UI), so what a surface becomes is decided in one
// place as before.
//
// A marker can arrive split across chunks, so a tail that could still grow into
// one is withheld rather than shown and then taken back.
type FencePreview struct {
	inside bool
	held   string
}

// Feed returns the part of chunk that may be shown.
func (preview *FencePreview) Feed(chunk string) string {
	if preview == nil {
		return chunk
	}
	pending := preview.held + chunk
	preview.held = ""
	var visible strings.Builder
	for {
		if preview.inside {
			end := strings.Index(pending, FenceEnd)
			if end < 0 {
				// Inside a fence nothing is shown; only a partial end marker has
				// to survive into the next chunk.
				preview.held = partialMarkerSuffix(pending, FenceEnd)
				break
			}
			pending = pending[end+len(FenceEnd):]
			preview.inside = false
			continue
		}
		start := strings.Index(pending, FenceStart)
		if start < 0 {
			held := partialMarkerSuffix(pending, FenceStart)
			visible.WriteString(pending[:len(pending)-len(held)])
			preview.held = held
			break
		}
		visible.WriteString(pending[:start])
		pending = pending[start+len(FenceStart):]
		preview.inside = true
	}
	return visible.String()
}

// Flush returns text withheld as a possible marker that never became one.
//
// Text held inside an unclosed fence stays dropped: it is the beginning of a
// surface body, and a body that never closes is dropped whole on the terminal
// path too.
func (preview *FencePreview) Flush() string {
	if preview == nil || preview.inside {
		return ""
	}
	held := preview.held
	preview.held = ""
	return held
}

// MaskFences strips fence spans from one complete string. For text that is shown
// rather than parsed — the partial text of an interrupted stream, say — where the
// caller has no chunks to feed.
func MaskFences(text string) string {
	var preview FencePreview
	return preview.Feed(text) + preview.Flush()
}

// partialMarkerSuffix returns the longest suffix of text that is a prefix of
// marker: the part that could still turn into a marker. A complete marker is not
// a candidate, since the caller handles that case first.
func partialMarkerSuffix(text, marker string) string {
	longest := len(marker) - 1
	if len(text) < longest {
		longest = len(text)
	}
	for size := longest; size > 0; size-- {
		if strings.HasPrefix(marker, text[len(text)-size:]) {
			return text[len(text)-size:]
		}
	}
	return ""
}
