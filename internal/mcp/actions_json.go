package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// UnmarshalJSON accepts the former selected-instance key while new requests serialize only the canonical instance field.
func (r *ActionRequest) UnmarshalJSON(data []byte) error {
	type canonical ActionRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return errors.New("action request must be a JSON object")
	}
	fields := map[string]json.RawMessage{}
	var legacy json.RawMessage
	hasInstance := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("action request contains a non-string key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		normalizedKey := key
		switch strings.ToLower(key) {
		case "profile":
			legacy = value
			continue
		case "instance", "protocol_version", "action", "target", "inputs", "package_dir", "state_dir", "interactive", "image_source", "release_image":
			normalizedKey = strings.ToLower(key)
			if normalizedKey == "instance" {
				hasInstance = true
			}
		}
		fields[normalizedKey] = value
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return errors.New("action request must contain one JSON object")
		}
		return err
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(normalized))
	strict.DisallowUnknownFields()
	var decoded canonical
	if err := strict.Decode(&decoded); err != nil {
		return err
	}
	if len(legacy) > 0 {
		legacyInstance := decoded.Instance
		if err := json.Unmarshal(legacy, &legacyInstance); err != nil {
			return err
		}
		if !hasInstance {
			decoded.Instance = legacyInstance
		}
	}
	*r = ActionRequest(decoded)
	return nil
}
