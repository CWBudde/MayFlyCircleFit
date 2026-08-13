package renderer

// dirtySpan is a half-open horizontal interval [start, end). Rows in a
// dirtySpanSet remain sorted, non-overlapping, and non-adjacent as spans are
// inserted, so every covered pixel is visited exactly once during cost update.
type dirtySpan struct {
	start int
	end   int
}

type dirtySpanSet struct {
	spans           []dirtySpan
	counts          []int
	rowCapacity     int
	height          int
	overflow        bool
	normalized      bool
	dirtyPixels     int
	mergedSpanCount int
}

// reset prepares storage for a render of at most spansPerRow circles. A circle
// contributes at most one horizontal span to a row, so the flat backing store
// is a hard, geometry-independent bound. Storage is retained between renders.
func (s *dirtySpanSet) reset(height, spansPerRow int) {
	if height < 0 {
		height = 0
	}
	if spansPerRow < 1 {
		spansPerRow = 1
	}
	required := height * spansPerRow
	if cap(s.spans) < required {
		s.spans = make([]dirtySpan, required)
	} else {
		s.spans = s.spans[:required]
	}
	if cap(s.counts) < height {
		s.counts = make([]int, height)
	} else {
		s.counts = s.counts[:height]
		clear(s.counts)
	}
	s.rowCapacity = spansPerRow
	s.height = height
	s.overflow = false
	s.normalized = false
	s.dirtyPixels = 0
	s.mergedSpanCount = 0
}

func (s *dirtySpanSet) add(y, start, end int) {
	if y < 0 || y >= s.height || end <= start || s.overflow {
		return
	}
	count := s.counts[y]
	if count == s.rowCapacity {
		s.overflow = true
		return
	}
	base := y * s.rowCapacity
	s.spans[base+count] = dirtySpan{start: start, end: end}
	s.counts[y] = count + 1
	s.normalized = false
}

func (s *dirtySpanSet) normalize() bool {
	if s.overflow {
		return false
	}
	if s.normalized {
		return true
	}
	s.dirtyPixels = 0
	s.mergedSpanCount = 0
	for y := 0; y < s.height; y++ {
		row := s.row(y)
		// Circle counts are deliberately small in the staged path. Insertion
		// sort avoids interface calls and is faster than sort.Slice here.
		for i := 1; i < len(row); i++ {
			span := row[i]
			j := i
			for j > 0 && row[j-1].start > span.start {
				row[j] = row[j-1]
				j--
			}
			row[j] = span
		}
		merged := 0
		for _, span := range row {
			if merged != 0 && span.start <= row[merged-1].end {
				row[merged-1].end = max(row[merged-1].end, span.end)
				continue
			}
			row[merged] = span
			merged++
		}
		s.counts[y] = merged
		s.mergedSpanCount += merged
		for _, span := range row[:merged] {
			s.dirtyPixels += span.end - span.start
		}
	}
	s.normalized = true
	return true
}

func (s *dirtySpanSet) row(y int) []dirtySpan {
	if y < 0 || y >= s.height || s.rowCapacity == 0 {
		return nil
	}
	base := y * s.rowCapacity
	return s.spans[base : base+s.counts[y]]
}

func (s *dirtySpanSet) metrics() (pixels, spans int) {
	if !s.normalize() {
		return 0, 0
	}
	return s.dirtyPixels, s.mergedSpanCount
}
