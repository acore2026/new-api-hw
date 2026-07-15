package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestValidateBoundedIntegerOption(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		maximum int
		wantErr bool
	}{
		{name: "zero", value: "0", maximum: common.MaxRetryTimes},
		{name: "retry maximum", value: "100", maximum: common.MaxRetryTimes},
		{name: "delay maximum", value: "60000", maximum: common.MaxRetryDelayMilliseconds},
		{name: "negative", value: "-1", maximum: common.MaxRetryTimes, wantErr: true},
		{name: "retry too large", value: "101", maximum: common.MaxRetryTimes, wantErr: true},
		{name: "delay too large", value: "60001", maximum: common.MaxRetryDelayMilliseconds, wantErr: true},
		{name: "fraction", value: "1.5", maximum: common.MaxRetryTimes, wantErr: true},
		{name: "not a number", value: "later", maximum: common.MaxRetryTimes, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBoundedIntegerOption(test.value, 0, test.maximum)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateBoundedIntegerOption(%q) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
		})
	}
}
