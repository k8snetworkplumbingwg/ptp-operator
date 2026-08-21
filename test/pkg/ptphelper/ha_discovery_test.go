package ptphelper

import (
	"testing"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
)

func TestConfigIsPhc2SysHa(t *testing.T) {
	emptyPtp4l := ""
	phc2sysOpts := "-a -r -m -l 7 -n 24"
	name := "test-dual-nic-bc-ha"

	tests := []struct {
		name    string
		profile ptpv1.PtpProfile
		want    bool
	}{
		{
			name: "HA with phc2sysOpts",
			profile: ptpv1.PtpProfile{
				Name:        &name,
				Ptp4lOpts:   &emptyPtp4l,
				Phc2sysOpts: &phc2sysOpts,
				PtpSettings: map[string]string{"haProfiles": "test-bc-master1,test-bc-master2"},
			},
			want: true,
		},
		{
			name: "HA without phc2sysOpts is not HA",
			profile: ptpv1.PtpProfile{
				Name:        &name,
				Ptp4lOpts:   &emptyPtp4l,
				Phc2sysOpts: nil,
				PtpSettings: map[string]string{"haProfiles": "test-bc-master1,test-bc-master2"},
			},
			want: false,
		},
		{
			name: "missing haProfiles",
			profile: ptpv1.PtpProfile{
				Name:        &name,
				Ptp4lOpts:   &emptyPtp4l,
				Phc2sysOpts: &phc2sysOpts,
				PtpSettings: map[string]string{},
			},
			want: false,
		},
		{
			name: "single haProfiles entry",
			profile: ptpv1.PtpProfile{
				Name:        &name,
				Ptp4lOpts:   &emptyPtp4l,
				Phc2sysOpts: &phc2sysOpts,
				PtpSettings: map[string]string{"haProfiles": "test-bc-master1"},
			},
			want: false,
		},
		{
			name: "non-empty ptp4lOpts",
			profile: ptpv1.PtpProfile{
				Name:        &name,
				Ptp4lOpts:   func() *string { s := "-2"; return &s }(),
				Phc2sysOpts: &phc2sysOpts,
				PtpSettings: map[string]string{"haProfiles": "test-bc-master1,test-bc-master2"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ptpv1.PtpConfig{}
			cfg.Name = name
			cfg.Spec.Profile = []ptpv1.PtpProfile{tt.profile}
			if got := ConfigIsPhc2SysHa(cfg); got != tt.want {
				t.Fatalf("ConfigIsPhc2SysHa() = %v, want %v", got, tt.want)
			}
		})
	}
}
