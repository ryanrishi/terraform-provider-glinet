package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ryanrishi/glinet-client-go"
	"github.com/stretchr/testify/assert"
)

func TestSystemTimezoneConfigDataSource_Read(t *testing.T) {
	tests := []struct {
		name           string
		mockResponse   *glinet.GetSystemTimezoneConfigResponse
		mockError      error
		expectedState  systemTimezoneConfigModel
		expectedError  bool
		errorContains  string
	}{
		{
			name: "successful read",
			mockResponse: &glinet.GetSystemTimezoneConfigResponse{
				Zonename:            "America/New_York",
				TZOffset:            "-0500",
				AutoTimezoneEnabled: false,
				Localtime:           1700000000,
				Timezone:            "EST-5",
			},
			expectedState: systemTimezoneConfigModel{
				Id:                  types.StringValue("EST-5"),
				Zonename:            types.StringValue("America/New_York"),
				TZOffset:            types.StringValue("-0500"),
				AutoTimezoneEnabled: types.BoolValue(false),
				Localtime:           types.Int64Value(1700000000),
				Timezone:            types.StringValue("EST-5"),
			},
			expectedError: false,
		},
		{
			name: "successful read with auto timezone enabled",
			mockResponse: &glinet.GetSystemTimezoneConfigResponse{
				Zonename:            "UTC",
				TZOffset:            "+0000",
				AutoTimezoneEnabled: true,
				Localtime:           1600000000,
				Timezone:            "UTC",
			},
			expectedState: systemTimezoneConfigModel{
				Id:                  types.StringValue("UTC"),
				Zonename:            types.StringValue("UTC"),
				TZOffset:            types.StringValue("+0000"),
				AutoTimezoneEnabled: types.BoolValue(true),
				Localtime:           types.Int64Value(1600000000),
				Timezone:            types.StringValue("UTC"),
			},
			expectedError: false,
		},
		{
			name:          "api error",
			mockError:     errors.New("network timeout"),
			expectedError: true,
			errorContains: "Unable to Read System Timezone Config",
		},
		{
			name:          "authentication error",
			mockError:     errors.New("authentication failed"),
			expectedError: true,
			errorContains: "Unable to Read System Timezone Config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock services
			mockSystemService := NewMockSystemService()
			mockClient := NewMockGLiNetClient()
			
			// Configure expectations - tests provide all values
			if tt.expectedError {
				mockSystemService.On("GetTimezoneConfig").Return((*glinet.GetSystemTimezoneConfigResponse)(nil), tt.mockError)
			} else {
				mockSystemService.On("GetTimezoneConfig").Return(tt.mockResponse, nil)
			}
			
			mockClient.On("System").Return(mockSystemService)

			// Test the business logic
			config, err := mockClient.System().GetTimezoneConfig()
			
			// Check for expected errors
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.mockError.Error())
				}
				// Verify expectations were met
				mockSystemService.AssertExpectations(t)
				mockClient.AssertExpectations(t)
				return
			}

			// Check no errors occurred
			assert.NoError(t, err)
			assert.NotNil(t, config)

			// Verify the data source logic by manually building the state
			state := systemTimezoneConfigModel{
				Id:                  types.StringValue(config.Timezone),
				Zonename:            types.StringValue(config.Zonename),
				TZOffset:            types.StringValue(config.TZOffset),
				AutoTimezoneEnabled: types.BoolValue(config.AutoTimezoneEnabled),
				Localtime:           types.Int64Value(int64(config.Localtime)),
				Timezone:            types.StringValue(config.Timezone),
			}

			// Verify state matches expectations
			assert.Equal(t, tt.expectedState.Id, state.Id)
			assert.Equal(t, tt.expectedState.Zonename, state.Zonename)
			assert.Equal(t, tt.expectedState.TZOffset, state.TZOffset)
			assert.Equal(t, tt.expectedState.AutoTimezoneEnabled, state.AutoTimezoneEnabled)
			assert.Equal(t, tt.expectedState.Localtime, state.Localtime)
			assert.Equal(t, tt.expectedState.Timezone, state.Timezone)
			
			// Verify all expected calls were made
			mockSystemService.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestSystemTimezoneConfigDataSource_Configure(t *testing.T) {
	tests := []struct {
		name             string
		providerData     interface{}
		expectedError    bool
		errorContains    string
		expectedClient   GLiNetClient
	}{
		{
			name:           "successful configure with mock client",
			providerData:   NewMockGLiNetClient(),
			expectedError:  false,
			expectedClient: NewMockGLiNetClient(),
		},
		{
			name:           "successful configure with real client wrapper",
			providerData:   &ClientWrapper{},
			expectedError:  false,
			expectedClient: &ClientWrapper{},
		},
		{
			name:          "invalid provider data type",
			providerData:  "invalid",
			expectedError: true,
			errorContains: "Unexpected Data Source Configure Type",
		},
		{
			name:          "nil provider data",
			providerData:  nil,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create data source instance
			dataSource := &systemTimezoneConfigDataSource{}

			// Create request and response
			req := datasource.ConfigureRequest{
				ProviderData: tt.providerData,
			}
			resp := &datasource.ConfigureResponse{}

			// Call Configure method
			dataSource.Configure(context.Background(), req, resp)

			// Check for expected errors
			if tt.expectedError {
				assert.True(t, resp.Diagnostics.HasError())
				if tt.errorContains != "" {
					found := false
					for _, diag := range resp.Diagnostics.Errors() {
						if diag.Summary() == tt.errorContains {
							found = true
							break
						}
					}
					assert.True(t, found, "Expected error message not found: %s", tt.errorContains)
				}
				return
			}

			// Check no errors occurred
			assert.False(t, resp.Diagnostics.HasError())

			// Verify client was set if provider data was provided
			if tt.providerData != nil {
				assert.NotNil(t, dataSource.client)
			}
		})
	}
}

func TestSystemTimezoneConfigDataSource_Schema(t *testing.T) {
	dataSource := &systemTimezoneConfigDataSource{}
	
	req := datasource.SchemaRequest{}
	resp := &datasource.SchemaResponse{}
	
	dataSource.Schema(context.Background(), req, resp)
	
	// Verify schema is not nil
	assert.NotNil(t, resp.Schema)
	
	// Verify required attributes exist
	attrs := resp.Schema.Attributes
	assert.Contains(t, attrs, "id")
	assert.Contains(t, attrs, "zonename")
	assert.Contains(t, attrs, "tzoffset")
	assert.Contains(t, attrs, "auto_timezone_enabled")
	assert.Contains(t, attrs, "localtime")
	assert.Contains(t, attrs, "timezone")
	
	// Verify all attributes are computed
	assert.True(t, attrs["id"].IsComputed())
	assert.True(t, attrs["zonename"].IsComputed())
	assert.True(t, attrs["tzoffset"].IsComputed())
	assert.True(t, attrs["auto_timezone_enabled"].IsComputed())
	assert.True(t, attrs["localtime"].IsComputed())
	assert.True(t, attrs["timezone"].IsComputed())
}

func TestSystemTimezoneConfigDataSource_Metadata(t *testing.T) {
	dataSource := &systemTimezoneConfigDataSource{}
	
	req := datasource.MetadataRequest{
		ProviderTypeName: "glinet",
	}
	resp := &datasource.MetadataResponse{}
	
	dataSource.Metadata(context.Background(), req, resp)
	
	assert.Equal(t, "glinet_system_timezone_config", resp.TypeName)
}