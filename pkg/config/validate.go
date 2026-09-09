package config

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/v0.1.json
var schemaSource []byte

var compiledSchema *jsonschema.Schema

func init() {
	// AddResource wants an already-decoded JSON value, not raw text — passing
	// the string itself makes the compiler see "the schema is a string".
	var doc interface{}
	if err := json.Unmarshal(schemaSource, &doc); err != nil {
		panic(fmt.Sprintf("decode rtunk config schema: %v", err))
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema/v0.1.json", doc); err != nil {
		panic(fmt.Sprintf("add rtunk config schema resource: %v", err))
	}
	sch, err := c.Compile("schema/v0.1.json")
	if err != nil {
		panic(fmt.Sprintf("compile rtunk config schema: %v", err))
	}
	compiledSchema = sch
}

// validate checks a generically-decoded config document against rtunk's own
// schema/v0.1.json. Trunk itself publishes a broader structural schema at
// https://static.trunk.io/pub/trunk-yaml-schema.json — that's the reference
// for "what shape does a real trunk.yaml have"; rtunk's schema additionally
// pins `version` to exactly 0.1, the only version currently accepted.
func validate(doc map[string]interface{}) error {
	if err := compiledSchema.Validate(doc); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}
