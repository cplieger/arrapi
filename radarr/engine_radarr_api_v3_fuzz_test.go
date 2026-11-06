package radarr

import (
	"encoding/json"
	"testing"
)

// FuzzIndexerFlags decodes arbitrary JSON as a release's indexerFlags: a list of names
// sets exactly the bits of Radarr's names in it, an integer keeps only Radarr's bits,
// and nothing else sets a bit.
func FuzzIndexerFlags(f *testing.F) {
	for _, s := range []string{
		`["G_Scene","G_Freeleech25"]`, `[]`, `null`, `["Unknown","0"]`, `640`, `4097`, `-1`, `1.5`,
		`"G_Scene"`, `{}`, `true`, `[1,null,{},"Nuked"]`, `["G_Freeleech","g_freeleech"]`, `4294967295`,
	} {
		f.Add([]byte(s))
	}
	var all IndexerFlags
	for _, bit := range radarrFlagBits {
		all |= bit
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var got radarrIndexerFlags
		if err := json.Unmarshal(data, &got); err != nil {
			return
		}
		if IndexerFlags(got)&^all != 0 {
			t.Fatalf("decode %s = %d, which sets a bit Radarr does not define", data, got)
		}
		var names []any
		if json.Unmarshal(data, &names) == nil && names != nil {
			var want IndexerFlags
			for _, n := range names {
				if s, ok := n.(string); ok {
					want |= radarrFlagBits[s]
				}
			}
			if IndexerFlags(got) != want {
				t.Fatalf("decode %s = %d, want %d", data, got, want)
			}
		}
		var n uint32
		if json.Unmarshal(data, &n) == nil && IndexerFlags(got) != IndexerFlags(n)&all {
			t.Fatalf("decode %s = %d, want %d", data, got, IndexerFlags(n)&all)
		}
	})
}
