package driver

import "testing"

func TestGeometryValidate(t *testing.T) {
	tests := map[string]struct {
		geometry Geometry
		wantErr  bool
	}{
		"valid": {
			geometry: Geometry{TotalSize: 1 << 20, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff},
		},
		"zero total": {
			geometry: Geometry{EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff},
			wantErr:  true,
		},
		"block smaller than page": {
			geometry: Geometry{TotalSize: 1 << 20, EraseBlockSize: 128, PageSize: 256, EraseValue: 0xff},
			wantErr:  true,
		},
		"partial erase block": {
			geometry: Geometry{TotalSize: 1000, EraseBlockSize: 256, PageSize: 128, EraseValue: 0xff},
			wantErr:  true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.geometry.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Geometry.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
