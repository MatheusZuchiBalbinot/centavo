package config

import "errors"

// exampleSecret is the placeholder every secret of .env.example carries; a
// process started with it would run on a value that is public.
const exampleSecret = "change-me"

// ErrExampleSecret means a secret was left at its .env.example value.
var ErrExampleSecret = errors.New("a secret still has the value of .env.example; run ./entrypoint.sh to generate real ones")

// refuseExampleSecrets fails on the first secret still set to the example value.
func refuseExampleSecrets(secrets ...string) error {
	for _, secret := range secrets {
		if secret == exampleSecret {
			return ErrExampleSecret
		}
	}

	return nil
}
