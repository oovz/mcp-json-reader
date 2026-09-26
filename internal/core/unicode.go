package core

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
)

// UnmarshalStrictString uses the standard streaming decoder's Unicode policy.
// Copy the token before advancing the decoder, whose tokens borrow its storage.
func UnmarshalStrictString(raw []byte, target *string) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() != '"' {
		return errors.New("expected a JSON string")
	}
	value := token.String()
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON string data")
	}
	*target = value
	return nil
}
