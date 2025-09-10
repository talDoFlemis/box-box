package main

import (
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCORSSettingsValidation(t *testing.T) {
	// Arrange
	validate := validator.New()
	allowedHeaders := map[string]struct{}{
		"Accept": {}, "Authorization": {}, "Content-Type": {}, "X-CSRF-Token": {},
	}
	validate.RegisterValidation("baseheader", func(fl validator.FieldLevel) bool {
		header := fl.Field().String()
		_, ok := allowedHeaders[header]
		return ok
	})

	tests := []struct {
		name    string
		cors    CORSSettings
		wantErr bool
	}{
		{
			name: "valid cors",
			cors: CORSSettings{
				Origins: []string{"https://example.com"},
				Methods: []string{"GET", "POST"},
				Headers: []string{"Accept", "Authorization"},
			},
			wantErr: false,
		},
		{
			name: "invalid method",
			cors: CORSSettings{
				Origins: []string{"https://example.com"},
				Methods: []string{"FOO"},
				Headers: []string{"Accept"},
			},
			wantErr: true,
		},
		{
			name: "invalid header",
			cors: CORSSettings{
				Origins: []string{"https://example.com"},
				Methods: []string{"GET"},
				Headers: []string{"X-INVALID"},
			},
			wantErr: true,
		},
		{
			name: "invalid origin",
			cors: CORSSettings{
				Origins: []string{"*"},
				Methods: []string{"GET"},
				Headers: []string{"Accept"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		// Act
		err := validate.Struct(tt.cors)

		// Assert
		if tt.wantErr {
			assert.Error(t, err, tt.name)
		} else {
			assert.NoError(t, err, tt.name)
		}
	}
}
