package config

import (
	"errors"
	"net/url"
)

// exampleSecret is the placeholder every secret of .env.example carries; a
// process started with it would run on a value that is public.
const exampleSecret = "change-me"

var ErrExampleSecret = errors.New("a secret still has the value of .env.example; run ./entrypoint.sh to generate real ones")

func refuseExampleSecrets(secrets ...string) error {
	for _, secret := range secrets {
		if secret == exampleSecret {
			return ErrExampleSecret
		}
	}

	return nil
}

func passwordOf(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		// An unparsable URL has no password to compare; the driver refuses it.
		return ""
	}

	if parsed.User == nil {
		return ""
	}

	password, _ := parsed.User.Password()

	return password
}
