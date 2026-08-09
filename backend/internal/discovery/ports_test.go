package discovery

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want []int
	}{
		{name: "single port", spec: "443", want: []int{443}},
		{name: "single port with spaces", spec: "  443  ", want: []int{443}},
		{name: "multiple singles sorted", spec: "8443,443,9443", want: []int{443, 8443, 9443}},
		{name: "range", spec: "8000-8004", want: []int{8000, 8001, 8002, 8003, 8004}},
		{name: "range of one", spec: "443-443", want: []int{443}},
		{name: "range with spaces around dash", spec: "8000 - 8002", want: []int{8000, 8001, 8002}},
		{name: "preset web", spec: "web", want: presetWeb},
		{name: "preset web uppercase", spec: "WEB", want: presetWeb},
		{name: "preset common", spec: "common", want: presetCommon},
		{name: "mixed single and range", spec: "443,8000-8002", want: []int{443, 8000, 8001, 8002}},
		{name: "mixed preset and single", spec: "web,9999", want: append(copyInts(presetWeb), 9999)},
		{name: "dedup identical singles", spec: "443,443,443", want: []int{443}},
		{name: "union of overlapping ranges", spec: "8000-8003,8002-8005", want: []int{8000, 8001, 8002, 8003, 8004, 8005}},
		{name: "union of overlapping preset and single", spec: "web,443", want: presetWeb},
		{name: "union of overlapping preset and range", spec: "web,8443-8445", want: append(copyInts(presetWeb), 8444, 8445)},
		{name: "empty tokens are skipped", spec: "443,,8443", want: []int{443, 8443}},
		{name: "trailing comma", spec: "443,", want: []int{443}},
		{name: "boundary low", spec: "1", want: []int{1}},
		{name: "boundary high", spec: "65535", want: []int{65535}},
		{name: "boundary range ends", spec: "1,65535", want: []int{1, 65535}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePortSpec(tt.spec)
			if err != nil {
				t.Fatalf("ParsePortSpec(%q) error = %v, want nil", tt.spec, err)
			}
			want := copyInts(tt.want)
			sort.Ints(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParsePortSpec(%q) = %v, want %v", tt.spec, got, want)
			}
		})
	}
}

func TestParsePortSpecResultIsSortedAndUnique(t *testing.T) {
	got, err := ParsePortSpec("common,web,443,8000-8002,8001")
	if err != nil {
		t.Fatalf("ParsePortSpec error = %v", err)
	}
	seen := make(map[int]bool, len(got))
	for i, p := range got {
		if seen[p] {
			t.Fatalf("중복 포트 %d", p)
		}
		seen[p] = true
		if i > 0 && got[i-1] >= p {
			t.Fatalf("정렬 안 됨: got[%d]=%d >= got[%d]=%d", i-1, got[i-1], i, p)
		}
	}
}

func TestParsePortSpecErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{name: "empty string", spec: ""},
		{name: "whitespace only", spec: "   "},
		{name: "only commas", spec: ",,,"},
		{name: "zero", spec: "0"},
		{name: "above max", spec: "65536"},
		{name: "way above max", spec: "70000"},
		{name: "negative", spec: "-1"},
		{name: "reversed range", spec: "9000-8000"},
		{name: "range start zero", spec: "0-100"},
		{name: "range end above max", spec: "65000-65536"},
		{name: "three part range", spec: "1-2-3"},
		{name: "unknown preset", spec: "webb"},
		{name: "not a number", spec: "https"},
		{name: "float", spec: "443.0"},
		{name: "valid token then invalid", spec: "443,70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePortSpec(tt.spec)
			if err == nil {
				t.Fatalf("ParsePortSpec(%q) = %v, want error", tt.spec, got)
			}
			if !errors.Is(err, ErrInvalidScanInput) {
				t.Errorf("error = %v, want wrapped ErrInvalidScanInput", err)
			}
		})
	}
}

func TestParsePortSpecAllPreset(t *testing.T) {
	got, err := ParsePortSpec("all")
	if err != nil {
		t.Fatalf("ParsePortSpec(\"all\") error = %v", err)
	}
	if len(got) != 65535 {
		t.Fatalf("len = %d, want 65535", len(got))
	}
	if got[0] != 1 {
		t.Errorf("first = %d, want 1", got[0])
	}
	if got[len(got)-1] != 65535 {
		t.Errorf("last = %d, want 65535", got[len(got)-1])
	}
	// all에 다른 토큰을 합쳐도 65535개를 넘지 않는다(합집합).
	merged, err := ParsePortSpec("all,web,443")
	if err != nil {
		t.Fatalf("ParsePortSpec error = %v", err)
	}
	if len(merged) != 65535 {
		t.Errorf("len(all,web,443) = %d, want 65535", len(merged))
	}
}

func TestPresetSizes(t *testing.T) {
	if len(presetWeb) != 6 {
		t.Errorf("web preset = %d ports, want 6: %v", len(presetWeb), presetWeb)
	}
	if len(presetCommon) != 26 {
		t.Errorf("common preset = %d ports, want 26: %v", len(presetCommon), presetCommon)
	}
	// common은 web의 상위집합이다.
	inCommon := make(map[int]bool, len(presetCommon))
	for _, p := range presetCommon {
		inCommon[p] = true
	}
	for _, p := range presetWeb {
		if !inCommon[p] {
			t.Errorf("web 포트 %d가 common에 없습니다", p)
		}
	}
}

// TestPresetsExcludeSTARTTLSPorts는 STARTTLS 승격 포트가 프리셋에 다시 들어오는 것을 막는다.
// ScanEndpoint는 implicit TLS 전용이라 이 포트들에서는 인증서를 얻을 수 없다 —
// 근거는 ports.go의 starttlsExcludedPorts 주석 참조. all은 사용자가 명시적으로
// 전체 포트를 요구한 경우이므로 이 검사 대상이 아니다.
func TestPresetsExcludeSTARTTLSPorts(t *testing.T) {
	presets := map[string][]int{
		PresetWeb:    presetWeb,
		PresetCommon: presetCommon,
	}
	for name, ports := range presets {
		set := make(map[int]bool, len(ports))
		for _, p := range ports {
			set[p] = true
		}
		for _, bad := range starttlsExcludedPorts {
			if set[bad] {
				t.Errorf("%s 프리셋에 STARTTLS 포트 %d가 있습니다 — ScanEndpoint는 implicit TLS 전용이라 인증서를 얻을 수 없습니다 (ports.go의 starttlsExcludedPorts 주석 참조)", name, bad)
			}
		}
	}
}

func TestPortPresets(t *testing.T) {
	presets := PortPresets()
	if len(presets) != 3 {
		t.Fatalf("len(PortPresets()) = %d, want 3", len(presets))
	}

	byName := make(map[string]PortPreset, len(presets))
	for _, p := range presets {
		if p.Description == "" {
			t.Errorf("%s 프리셋에 설명이 없습니다", p.Name)
		}
		byName[p.Name] = p
	}

	web, ok := byName[PresetWeb]
	if !ok {
		t.Fatalf("web 프리셋 없음")
	}
	if web.Count != 6 || len(web.Ports) != 6 {
		t.Errorf("web = count %d / %d ports, want 6 / 6", web.Count, len(web.Ports))
	}

	common, ok := byName[PresetCommon]
	if !ok {
		t.Fatalf("common 프리셋 없음")
	}
	if common.Count != 26 || len(common.Ports) != 26 {
		t.Errorf("common = count %d / %d ports, want 26 / 26", common.Count, len(common.Ports))
	}

	all, ok := byName[PresetAll]
	if !ok {
		t.Fatalf("all 프리셋 없음")
	}
	if all.Count != 65535 {
		t.Errorf("all count = %d, want 65535", all.Count)
	}
	if all.Ports != nil {
		t.Errorf("all 프리셋은 Ports를 싣지 않아야 합니다 (%d개)", len(all.Ports))
	}

	// 반환값은 복사본이라 호출자가 수정해도 내부 프리셋이 오염되지 않는다.
	common.Ports[0] = 1
	if presetCommon[0] == 1 {
		t.Error("PortPresets()가 내부 프리셋 슬라이스를 그대로 노출했습니다")
	}
}

func TestDefaultPortSpecParses(t *testing.T) {
	got, err := ParsePortSpec(DefaultPortSpec)
	if err != nil {
		t.Fatalf("ParsePortSpec(DefaultPortSpec) error = %v", err)
	}
	if !reflect.DeepEqual(got, copyInts(presetCommon)) {
		t.Errorf("기본값 %q가 common 프리셋과 다릅니다", DefaultPortSpec)
	}
}
