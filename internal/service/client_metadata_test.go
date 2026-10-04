package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeClientSourceMetadataAcceptsClientExif(t *testing.T) {
	updates, err := normalizeClientSourceMetadata(map[string]any{
		"width":        64,
		"height":       64,
		"exif_version": 2,
		"exif": map[string]any{
			"Model":           "Pixel 8",
			"DateTime":        "2026:10:05 12:34:56",
			"ISOSpeedRatings": "125",
		},
	})
	if err != nil {
		t.Fatalf("normalizeClientSourceMetadata() error = %v", err)
	}
	if got, _ := updates["exif_version"].(int); got != clientExifVersion {
		t.Fatalf("exif_version = %#v, want %d", updates["exif_version"], clientExifVersion)
	}
	exif, ok := updates["exif"].(map[string]any)
	if !ok {
		t.Fatalf("exif = %#v, want map", updates["exif"])
	}
	if exif["Model"] != "Pixel 8" || exif["DateTime"] != "2026:10:05 12:34:56" || exif["ISOSpeedRatings"] != "125" {
		t.Fatalf("exif = %#v, want the client's tags", exif)
	}
}

func TestNormalizeClientSourceMetadataExifOnlyPayload(t *testing.T) {
	// EXIF alone is usable metadata, so it must not trip the empty-payload guard.
	updates, err := normalizeClientSourceMetadata(map[string]any{
		"exif_version": 2,
		"exif":         map[string]any{"Model": "Pixel 8"},
	})
	if err != nil {
		t.Fatalf("normalizeClientSourceMetadata() error = %v", err)
	}
	if _, ok := updates["exif"]; !ok {
		t.Fatalf("updates = %#v, want exif", updates)
	}
}

func TestNormalizeClientSourceMetadataExifValidation(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantErr bool
		// wantExif is checked only when wantErr is false and the payload had an
		// accepted version.
		wantExif bool
	}{
		{
			name:    "version 2 with non-string value",
			raw:     map[string]any{"exif_version": 2, "exif": map[string]any{"Model": 8}},
			wantErr: true,
		},
		{
			name:    "version 2 with empty value",
			raw:     map[string]any{"exif_version": 2, "exif": map[string]any{"Model": ""}},
			wantErr: true,
		},
		{
			name:    "exif not an object",
			raw:     map[string]any{"exif_version": 2, "exif": "Model=Pixel 8"},
			wantErr: true,
		},
		{
			name:    "empty exif map is not metadata",
			raw:     map[string]any{"exif": map[string]any{}},
			wantErr: true,
		},
		{
			name:     "unsupported version ignores exif",
			raw:      map[string]any{"width": 64, "exif_version": 1, "exif": map[string]any{"ifd0-Model": "Pixel 8"}},
			wantErr:  false,
			wantExif: false,
		},
		{
			name:     "non-integer version ignores exif",
			raw:      map[string]any{"width": 64, "exif_version": 2.5, "exif": map[string]any{"Model": "Pixel 8"}},
			wantErr:  false,
			wantExif: false,
		},
		{
			name:     "missing version still accepts v2 exif",
			raw:      map[string]any{"exif": map[string]any{"Model": "Pixel 8"}},
			wantErr:  false,
			wantExif: true,
		},
		{
			name:    "too many tags",
			raw:     map[string]any{"exif_version": 2, "exif": tooManyExifTags(maxClientExifKeys + 1)},
			wantErr: true,
		},
		{
			name:    "overlong value",
			raw:     map[string]any{"exif_version": 2, "exif": map[string]any{"Model": strings.Repeat("x", maxClientExifValBytes+1)}},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := normalizeClientSourceMetadata(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeClientSourceMetadata(%#v) = %#v, want error", tc.raw, updates)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeClientSourceMetadata(%#v) error = %v", tc.raw, err)
			}
			if _, ok := updates["exif"]; ok != tc.wantExif {
				t.Fatalf("updates = %#v, want exif stored = %v", updates, tc.wantExif)
			}
		})
	}
}

func tooManyExifTags(count int) map[string]any {
	exif := make(map[string]any, count)
	for i := range count {
		exif[fmt.Sprintf("Tag%d", i)] = "v"
	}
	return exif
}
