package archify

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestASignatureGivesItsParametersAndReturnWhicheverSideItIsWritten(t *testing.T) {
	tests := []struct {
		name, signature, method, params, returns string
	}{
		{"TypeScript", "protected total(x: number, y: Tax): Price", "total", "x: number, y: Tax", "Price"},
		{"Python", "def total(self, rate: float) -> int", "total", "self, rate: float", "int"},
		{"Go", "func (c *Cart) Total(rate float64) (int, error)", "Total", "rate float64", "(int, error)"},
		{"Java", "public static List<Item> items(int limit)", "items", "int limit", "List<Item>"},
		{"Dart", "Future<void> save(Cart cart)", "save", "Cart cart", "Future<void>"},
		{"a constructor", "public Cart()", "Cart", "", ""},
		{"nested parentheses", "run(cb: (x: number) => void): void", "run", "cb: (x: number) => void", "void"},
		{"no parentheses at all", "int get total", "total", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, returns := splitSignature(tt.signature, tt.method)
			assert.Equal(t, tt.params, params)
			assert.Equal(t, tt.returns, returns)
		})
	}
}

func TestATypeNameBecomesAnArchifyID(t *testing.T) {
	taken := map[string]bool{}
	assert.Equal(t, "Cart", uniqueID("Cart", taken))
	assert.Equal(t, "Cart_2", uniqueID("Cart", taken), "two types of one name are two boxes")
	assert.Equal(t, "t_2D", uniqueID("2D", taken), "an id starts with a letter")
	assert.Equal(t, "Map_K_V", uniqueID("Map<K,V>", taken))
}

func TestABoxSitsBelowWhatItExtendsAndImplements(t *testing.T) {
	types := []classType{{ID: "Shop"}, {ID: "Base"}, {ID: "Cart"}, {ID: "Loose"}}
	placeOnGrid(types, []classRelationship{
		{From: "Base", To: "Shop", Kind: "realization"},
		{From: "Cart", To: "Base", Kind: "inheritance"},
		{From: "Cart", To: "Loose", Kind: "composition"},
	})
	rows := map[string]int{}
	for _, b := range types {
		rows[b.ID] = b.Row
	}
	assert.Less(t, rows["Shop"], rows["Base"])
	assert.Less(t, rows["Base"], rows["Cart"])
	assert.Equal(t, rows["Shop"], rows["Loose"], "holding a type does not put it above")
}
