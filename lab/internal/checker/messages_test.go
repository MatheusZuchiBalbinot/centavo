package checker_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// portugueseLetters are the letters only a Portuguese text has; a code string
// never needs them.
var portugueseLetters = regexp.MustCompile(`[áàâãéêíóôõúçÁÉÍÓÚÇ]`)

func TestMessages_LiveOnlyInTheirCatalogs(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{".", "../replay", "../dashboard"} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}

		for _, path := range paths {
			isExempt := strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "messages.go"
			if isExempt {
				continue
			}

			for _, literal := range portugueseLiterals(t, path) {
				t.Errorf("%s: %s belongs in messages.go", path, literal)
			}
		}
	}
}

func portugueseLiterals(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var found []string

	ast.Inspect(file, func(node ast.Node) bool {
		literal, isLiteral := node.(*ast.BasicLit)

		isPortugueseText := isLiteral && literal.Kind == token.STRING && portugueseLetters.MatchString(literal.Value)
		if isPortugueseText {
			found = append(found, fset.Position(literal.Pos()).String())
		}

		return true
	})

	return found
}
