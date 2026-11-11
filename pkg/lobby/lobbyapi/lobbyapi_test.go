package lobbyapi

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/zproxy_api.json is also decoded by zproxy's copy of these types (src/zproxy), so
// both sides read and write the same keys.
func TestZproxyAPIKeys(t *testing.T) {
	raw, err := os.ReadFile("testdata/zproxy_api.json")
	if err != nil {
		t.Fatal(err)
	}
	var bodies map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bodies); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{
		"RegisterProxyRequest":  &RegisterProxyRequest{},
		"RegisterProxyResponse": &RegisterProxyResponse{},
		"BattleInfoRequest":     &BattleInfoRequest{},
		"BattleInfoResponse":    &BattleInfoResponse{},
	} {
		dec := json.NewDecoder(bytes.NewReader(bodies[name]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out, _ := json.Marshal(v)
		var want, got any
		json.Unmarshal(bodies[name], &want)
		json.Unmarshal(out, &got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s re-encodes to\n%s\nwant\n%s", name, out, bodies[name])
		}
	}
}
