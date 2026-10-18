package dashboard

import "testing"

func TestFormatCents(t *testing.T) {
	t.Parallel()

	cases := map[int64]string{
		0:          "R$ 0,00",
		5:          "R$ 0,05",
		123_456:    "R$ 1.234,56",
		-100:       "-R$ 1,00",
		1234567890: "R$ 12.345.678,90",
	}

	for cents, want := range cases {
		if got := formatCents(cents); got != want {
			t.Errorf("formatCents(%d) = %q, want %q", cents, got, want)
		}
	}
}
