package dashboard

import "fmt"

type viewport struct {
	offset       int
	followBottom bool
}

func newViewport() viewport {
	return viewport{followBottom: true}
}

func viewportMax(total, height int) int {
	if total <= height || height <= 0 {
		return 0
	}
	return total - height
}

func (v *viewport) setContent(total, height int) {
	maximum := viewportMax(total, height)
	if v.followBottom {
		v.offset = maximum
	}
	if v.offset < 0 {
		v.offset = 0
	}
	if v.offset > maximum {
		v.offset = maximum
	}
	v.followBottom = v.offset == maximum
}

func (v *viewport) scroll(delta, total, height int) {
	v.offset += delta
	maximum := viewportMax(total, height)
	if v.offset < 0 {
		v.offset = 0
	}
	if v.offset > maximum {
		v.offset = maximum
	}
	v.followBottom = v.offset == maximum
}

func (v *viewport) ensureVisible(index, total, height int) {
	if height <= 0 || total <= 0 {
		v.offset = 0
		return
	}
	if index < v.offset {
		v.offset = index
	} else if index >= v.offset+height {
		v.offset = index - height + 1
	}
	maximum := viewportMax(total, height)
	if v.offset < 0 {
		v.offset = 0
	}
	if v.offset > maximum {
		v.offset = maximum
	}
	v.followBottom = false
}

func (v *viewport) top() {
	v.offset = 0
	v.followBottom = false
}

func (v *viewport) bottom(total, height int) {
	v.offset = viewportMax(total, height)
	v.followBottom = true
}

func (v viewport) visible(lines []string, height int) []string {
	if height <= 0 || len(lines) == 0 {
		return nil
	}
	maximum := viewportMax(len(lines), height)
	start := v.offset
	if start < 0 {
		start = 0
	}
	if start > maximum {
		start = maximum
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

func (v viewport) position(total, height int) string {
	if total == 0 {
		return "0/0"
	}
	start := v.offset
	maximum := viewportMax(total, height)
	if start > maximum {
		start = maximum
	}
	end := start + height
	if end > total {
		end = total
	}
	return fmt.Sprintf("%d-%d/%d", start+1, end, total)
}
