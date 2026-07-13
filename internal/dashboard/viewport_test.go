package dashboard

import (
	"reflect"
	"testing"
)

func TestViewportFollowsBottomAndPreservesReadingPosition(t *testing.T) {
	v := newViewport()
	v.setContent(6, 3)
	if v.offset != 3 || !v.followBottom {
		t.Fatalf("initial viewport = %#v, want bottom offset 3", v)
	}
	v.scroll(-1, 6, 3)
	if v.offset != 2 || v.followBottom {
		t.Fatalf("scrolled viewport = %#v, want offset 2 not following", v)
	}
	v.setContent(8, 3)
	if v.offset != 2 {
		t.Fatalf("grown content offset = %d, want 2", v.offset)
	}
	v.bottom(8, 3)
	v.setContent(10, 3)
	if v.offset != 7 || !v.followBottom {
		t.Fatalf("following viewport = %#v, want bottom offset 7", v)
	}
}

func TestViewportClampsSlicesAndReportsPosition(t *testing.T) {
	lines := []string{"0", "1", "2", "3", "4"}
	v := viewport{offset: 4}
	v.setContent(len(lines), 2)
	if got := v.visible(lines, 2); !reflect.DeepEqual(got, []string{"3", "4"}) {
		t.Fatalf("visible = %#v", got)
	}
	if got := v.position(len(lines), 2); got != "4-5/5" {
		t.Fatalf("position = %q, want 4-5/5", got)
	}
	v.top()
	v.scroll(20, len(lines), 2)
	if v.offset != 3 || !v.followBottom {
		t.Fatalf("clamped viewport = %#v", v)
	}
}

func TestViewportEnsuresSelectedRowIsVisible(t *testing.T) {
	v := viewport{}
	v.ensureVisible(5, 10, 3)
	if v.offset != 3 || v.followBottom {
		t.Fatalf("viewport = %#v, want offset 3", v)
	}
	v.ensureVisible(2, 10, 3)
	if v.offset != 2 {
		t.Fatalf("viewport offset = %d, want 2", v.offset)
	}
}
