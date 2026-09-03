package v1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPtpConfigValidator_MinOffsetThresholdAccepted(t *testing.T) {
	profileName := "test-profile"
	ptpConfig := &PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ptpconfig",
			Namespace: "openshift-ptp",
		},
		Spec: PtpConfigSpec{
			Profile: []PtpProfile{
				{
					Name: &profileName,
					PtpClockThreshold: &PtpClockThreshold{
						HoldOverTimeout:    5,
						MaxOffsetThreshold: 100,
						MinOffsetThreshold: -100,
					},
				},
			},
		},
	}

	validator := &ptpConfigValidator{}
	ctx := context.Background()

	warnings, err := validator.ValidateCreate(ctx, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)

	warnings, err = validator.ValidateUpdate(ctx, ptpConfig, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
}
