package ptphelper

import (
	"reflect"
	"testing"
)

func TestNicBaseName(t *testing.T) {
	tests := []struct {
		iface string
		want  string
	}{
		{iface: "ens1f0", want: "ens1f"},
		{iface: "ens1f1", want: "ens1f"},
		{iface: "ens7f0np0", want: "ens7f"},
		{iface: "ens7f1np1", want: "ens7f"},
		{iface: "ens7f2np2", want: "ens7f"},
		{iface: "ens4f0np0", want: "ens4f"},
		{iface: "ens4f3np3", want: "ens4f"},
	}
	for _, tc := range tests {
		t.Run(tc.iface, func(t *testing.T) {
			if got := nicBaseName(tc.iface); got != tc.want {
				t.Fatalf("nicBaseName(%q) = %q, want %q", tc.iface, got, tc.want)
			}
		})
	}
}

func TestAddAllInterfacesForNic(t *testing.T) {
	tests := []struct {
		name       string
		wpcIfaces  map[string]string
		firstIface string
		want       []string
	}{
		{
			name: "legacy ens1f naming",
			wpcIfaces: map[string]string{
				"ens1f0": "ens1f0",
				"ens1f1": "ens1f1",
			},
			firstIface: "ens1f0",
			want:       []string{"ens1f0", "ens1f1"},
		},
		{
			name: "netdev ens7f np naming finds peer port",
			wpcIfaces: map[string]string{
				"ens7f0np0": "ens7f0np0",
				"ens7f1np1": "ens7f1np1",
			},
			firstIface: "ens7f0np0",
			want:       []string{"ens7f0np0", "ens7f1np1"},
		},
		{
			name: "does not mix distinct NICs",
			wpcIfaces: map[string]string{
				"ens2f0np0": "ens2f0np0",
				"ens2f1np1": "ens2f1np1",
				"ens4f0np0": "ens4f0np0",
				"ens4f1np1": "ens4f1np1",
			},
			firstIface: "ens2f0np0",
			want:       []string{"ens2f0np0", "ens2f1np1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addAllInterfacesForNic(tc.wpcIfaces, tc.firstIface)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("addAllInterfacesForNic() = %v, want %v", got, tc.want)
			}
		})
	}
}
