package desc

import "testing"

// TestSnakeCase tests the SnakeCase function with various inputs and outputs
func TestSnakeCase(t *testing.T) {
	// Define a table of test cases
	testCases := []struct {
		input  string // input string
		output string // expected output string
	}{
		{"userId", "user_id"},
		{"userID", "user_id"},
		{"id", "id"},
		{"ID", "id"},
		{"ProviderAPIKey", "provider_api_key"},
		{"Option", "option"},
		{"CustomerHealthData", "customer_health_data"},
	}

	// Loop over the test cases
	for _, tc := range testCases {
		// Call the SnakeCase function with the input
		result := SnakeCase(tc.input)
		// Compare the result with the expected output
		if result != tc.output {
			// Report an error if they don't match
			t.Errorf("SnakeCase(%q) = %q, want %q", tc.input, result, tc.output)
		}
	}
}

// TestPascalCase tests the PascalCase function with various inputs and outputs
func TestPascalCase(t *testing.T) {
	// Define a table of test cases
	testCases := []struct {
		input  string // input string
		output string // expected output string
	}{
		{"user_id", "UserID"},
		{"id", "ID"},
		{"provider_api_key", "ProviderAPIKey"},
		{"customer_provider", "CustomerProvider"},
		{"url", "URL"},
		{"api", "API"},
	}

	// Loop over the test cases
	for _, tc := range testCases {
		// Call the PascalCase function with the input
		result := PascalCase(tc.input)
		// Compare the result with the expected output
		if result != tc.output {
			// Report an error if they don't match
			t.Errorf("PascalCase(%q) = %q, want %q", tc.input, result, tc.output)
		}
	}
}
