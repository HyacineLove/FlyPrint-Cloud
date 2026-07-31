package business

import "testing"

func TestQuotaUsageCountsPhysicalSheetsAndColorMultiplier(t *testing.T) {
	tests := []struct {
		name           string
		pages          int
		copies         int
		duplexMode     string
		colorMode      string
		expectedSheets int
		expectedPoints int
	}{
		{
			name: "simplex mono", pages: 3, copies: 2,
			duplexMode: "simplex", colorMode: "mono",
			expectedSheets: 6, expectedPoints: 6,
		},
		{
			name: "duplex odd mono", pages: 3, copies: 2,
			duplexMode: "longedge", colorMode: "mono",
			expectedSheets: 4, expectedPoints: 4,
		},
		{
			name: "duplex odd color", pages: 3, copies: 2,
			duplexMode: "shortedge", colorMode: "color",
			expectedSheets: 4, expectedPoints: 8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sheets, points, err := QuotaUsage(
				test.pages,
				test.copies,
				test.duplexMode,
				test.colorMode,
			)
			if err != nil {
				t.Fatalf("QuotaUsage() error = %v", err)
			}
			if sheets != test.expectedSheets || points != test.expectedPoints {
				t.Fatalf(
					"QuotaUsage() = sheets %d, points %d; want sheets %d, points %d",
					sheets,
					points,
					test.expectedSheets,
					test.expectedPoints,
				)
			}
		})
	}
}

func TestSettledQuotaUsageDoesNotShareOddSheetAcrossCopies(t *testing.T) {
	sheets, points, err := SettledQuotaUsage(3, 2, 4, "longedge", "mono")
	if err != nil {
		t.Fatalf("SettledQuotaUsage() error = %v", err)
	}
	if sheets != 3 || points != 3 {
		t.Fatalf(
			"SettledQuotaUsage() = sheets %d, points %d; want sheets 3, points 3",
			sheets,
			points,
		)
	}
}

func TestSettledQuotaUsageRejectsImpossibleImpressionCount(t *testing.T) {
	if _, _, err := SettledQuotaUsage(3, 2, 7, "simplex", "mono"); err == nil {
		t.Fatal("SettledQuotaUsage() error = nil, want invalid impression count")
	}
}

func TestQuotaUsageRejectsUnsupportedPrintModes(t *testing.T) {
	tests := []struct {
		name       string
		duplexMode string
		colorMode  string
	}{
		{name: "duplex", duplexMode: "booklet", colorMode: "mono"},
		{name: "color", duplexMode: "simplex", colorMode: "sepia"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := QuotaUsage(1, 1, test.duplexMode, test.colorMode); err == nil {
				t.Fatal("QuotaUsage() error = nil, want unsupported mode error")
			}
		})
	}
}
