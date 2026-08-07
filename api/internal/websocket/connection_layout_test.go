package websocket

import "testing"

func TestNormalizeInteractivePrintLayoutOptions(t *testing.T) {
	tests := []struct {
		name          string
		orientation   interface{}
		scale         interface{}
		wantDirection string
		wantScale     int
		wantError     bool
	}{
		{name: "defaults", wantDirection: "portrait", wantScale: 100},
		{name: "landscape and minimum", orientation: "landscape", scale: float64(50), wantDirection: "landscape", wantScale: 50},
		{name: "maximum", orientation: "portrait", scale: float64(150), wantDirection: "portrait", wantScale: 150},
		{name: "invalid direction", orientation: "sideways", scale: float64(100), wantError: true},
		{name: "invalid scale range", orientation: "portrait", scale: float64(40), wantError: true},
		{name: "invalid scale step", orientation: "portrait", scale: float64(55), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDirection, directionErr := normalizeInteractiveOrientation(tt.orientation)
			gotScale, scaleErr := normalizeInteractiveScalePercent(tt.scale)
			if tt.wantError {
				if directionErr == nil && scaleErr == nil {
					t.Fatal("expected invalid layout options to be rejected")
				}
				return
			}
			if directionErr != nil || scaleErr != nil {
				t.Fatalf("unexpected validation error: direction=%v scale=%v", directionErr, scaleErr)
			}
			if gotDirection != tt.wantDirection || gotScale != tt.wantScale {
				t.Fatalf("got direction=%q scale=%d, want direction=%q scale=%d", gotDirection, gotScale, tt.wantDirection, tt.wantScale)
			}
		})
	}
}
