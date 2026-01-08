package crypto

import "testing"

func TestDecryptAESCBC(t *testing.T) {
	// ciphertexts from Contrabass sample (Base64(IV+cipher)), key is 16 bytes
	key := "conbaEncrypt2025"

	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "admin password",
			input:  "dJsHjBidm1Egme0pkbTPUx6DuWoEs0tjBI+GPC34AaQPC34aCqfUBl0PKYuQAZXk",
			expect: "CloudExpert2025!",
		},
		{
			name:   "rabbitmq password",
			input:  "L/8l0UVhuCzYgfSfdKxV4mD5GFaDJbSRK0b42y76paM=",
			expect: "wotjfcl1013!",
		},
	}

	for _, tc := range cases {
		got, err := DecryptAESCBC(tc.input, key)
		if err != nil {
			t.Fatalf("case %s: decrypt error: %v", tc.name, err)
		}
		if got != tc.expect {
			t.Fatalf("case %s: expected %q, got %q", tc.name, tc.expect, got)
		}
	}
}
