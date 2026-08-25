package v1

import (
	"reflect"
	"sort"
	"testing"
)

func TestGetInterfaces(t *testing.T) {
	iface := "eth0"
	emptyConf := ""
	bcConf := `[global]
logging_level 6
[ens1f0]
masterOnly 0
[ens1f1]
masterOnly 1
`

	tests := []struct {
		name string
		cfg  PtpConfig
		mode PtpRole
		want []string
	}{
		{
			name: "no profiles",
			cfg:  PtpConfig{},
			mode: Slave,
			want: nil,
		},
		{
			name: "HA profile with nil Ptp4lConf",
			cfg: PtpConfig{
				Spec: PtpConfigSpec{
					Profile: []PtpProfile{{}},
				},
			},
			mode: Master,
			want: nil,
		},
		{
			name: "HA profile with empty Ptp4lConf",
			cfg: PtpConfig{
				Spec: PtpConfigSpec{
					Profile: []PtpProfile{{Ptp4lConf: &emptyConf}},
				},
			},
			mode: Slave,
			want: nil,
		},
		{
			name: "empty Ptp4lConf with Interface falls back for Slave",
			cfg: PtpConfig{
				Spec: PtpConfigSpec{
					Profile: []PtpProfile{{
						Ptp4lConf: &emptyConf,
						Interface: &iface,
					}},
				},
			},
			mode: Slave,
			want: []string{"eth0"},
		},
		{
			name: "BC config returns slave interfaces",
			cfg: PtpConfig{
				Spec: PtpConfigSpec{
					Profile: []PtpProfile{{Ptp4lConf: &bcConf}},
				},
			},
			mode: Slave,
			want: []string{"ens1f0"},
		},
		{
			name: "BC config returns master interfaces",
			cfg: PtpConfig{
				Spec: PtpConfigSpec{
					Profile: []PtpProfile{{Ptp4lConf: &bcConf}},
				},
			},
			mode: Master,
			want: []string{"ens1f1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetInterfaces(tt.cfg, tt.mode)
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("GetInterfaces() = %v, want %v", got, want)
			}
		})
	}
}

func TestPopulatePtp4lConfEmpty(t *testing.T) {
	conf := &Ptp4lConf{}
	empty := ""
	if err := conf.PopulatePtp4lConf(&empty, nil); err == nil {
		t.Fatal("expected error for empty ptp4l conf without sections")
	}
}
