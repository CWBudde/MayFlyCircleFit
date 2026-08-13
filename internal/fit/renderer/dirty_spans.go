package renderer

// dirtySpan is a half-open horizontal interval [start, end). Rows in a
// dirtySpanSet remain sorted, non-overlapping, and non-adjacent as spans are
// inserted, so every covered pixel is visited exactly once during cost update.
type dirtySpan struct {
	start int
	end   int
}

type dirtySpanSet struct {
	rows [][]dirtySpan
}

func (s *dirtySpanSet) reset(height int) {
	if cap(s.rows) < height {
		s.rows = make([][]dirtySpan, height)
	} else {
		s.rows = s.rows[:height]
		for y := range s.rows {
			s.rows[y] = s.rows[y][:0]
		}
	}
}

func (s *dirtySpanSet) add(y, start, end int) {
	if y < 0 || y >= len(s.rows) || end <= start {
		return
	}
	row := s.rows[y]
	insert := 0
	for insert < len(row) && row[insert].end < start {
		insert++
	}

	if insert == len(row) {
		s.rows[y] = append(row, dirtySpan{start: start, end: end})
		return
	}
	if end < row[insert].start {
		row = append(row, dirtySpan{})
		copy(row[insert+1:], row[insert:])
		row[insert] = dirtySpan{start: start, end: end}
		s.rows[y] = row
		return
	}

	row[insert].start = min(row[insert].start, start)
	row[insert].end = max(row[insert].end, end)
	consumeEnd := insert + 1
	for consumeEnd < len(row) && row[consumeEnd].start <= row[insert].end {
		row[insert].end = max(row[insert].end, row[consumeEnd].end)
		consumeEnd++
	}
	if consumeEnd > insert+1 {
		copy(row[insert+1:], row[consumeEnd:])
		row = row[:len(row)-(consumeEnd-insert-1)]
	}
	s.rows[y] = row
}

func (s *dirtySpanSet) metrics() (pixels, spans int) {
	for _, row := range s.rows {
		spans += len(row)
		for _, span := range row {
			pixels += span.end - span.start
		}
	}
	return pixels, spans
}
