package releaseparse

import (
	"encoding/json"
	"testing"
)

func TestResultJSONContract(t *testing.T) {
	for _, raw := range []string{
		`{"schemaVersion":1,"kind":"unknown","rawEpisode":"","episode":null,"resolution":null,"versionLabel":"","spec":{}}`,
		`{"schemaVersion":1,"kind":"episode","rawEpisode":"00v2","episode":0,"resolution":"1080p","versionLabel":"v2","spec":{"resolution":"1080p","languages":["zh-Hans"],"subtitleMode":"soft","codec":"h265","container":"mkv","bitDepth":"10"}}`,
		`{"schemaVersion":1,"kind":"special","rawEpisode":"SP01","episode":null,"resolution":"unknown","versionLabel":"","spec":{},"specAmbiguous":true}`,
	} {
		var got Result
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("persisted result no longer validates: %+v, %v", got, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != raw {
			t.Fatalf("parse JSON changed: %s, %v", encoded, err)
		}
	}
}
