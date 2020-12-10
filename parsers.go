package supervisor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"strings"
)

// MakeJsonLineParser is called with an io.Reader, and returns a function, that when called will output references to
// map[string]interface{} objects that contain the parsed json data.
// If an invalid json is encountered, all the characters up until a new-line will be dropped.
func MakeJsonLineParser(fromR io.Reader, bufferSize int) ProduceFn {
	br := bufio.NewReaderSize(fromR, bufferSize)
	dec := json.NewDecoder(br)
	return func() (*interface{}, error) {
		var v interface{}
		if err := dec.Decode(&v); err == nil {
			return &v, nil
		} else if err == io.EOF {
			return nil, err
		}

		rest, _ := ioutil.ReadAll(dec.Buffered())
		restLines := bytes.SplitAfterN(rest, []byte{'\n'}, 2)
		if len(restLines) > 1 {
			// todo: test memory consumption on many mistakes (which will happen)
			dec = json.NewDecoder(io.MultiReader(bytes.NewReader(restLines[1]), br))
		} else {
			dec = json.NewDecoder(br)
		}
		return nil, nil
	}
}

// MakeLineParser is called with an io.Reader, and returns a function, that when called will output references to
// strings that contain the bytes read from the io.Reader (without the new-line suffix).
func MakeLineParser(fromR io.Reader, bufferSize int) ProduceFn {
	br := bufio.NewReaderSize(fromR, bufferSize)
	return func() (*interface{}, error) {
		str, err := br.ReadString('\n')
		if err == nil {
			res := (interface{})(strings.TrimSuffix(str, string('\n')))
			return &res, nil
		}
		return nil, err
	}
}

// MakeLineParser is called with an io.Reader, and returns a function, that when called will output references to
// byte slices that contain the bytes read from the io.Reader.
func MakeBytesParser(fromR io.Reader, bufferSize int) ProduceFn {
	br := bufio.NewReaderSize(fromR, bufferSize)
	return func() (*interface{}, error) {
		v, err := br.ReadBytes('\n')
		if err == nil {
			res := (interface{})(bytes.TrimSuffix(v, []byte{'\n'}))
			return &res, nil
		}
		return nil, err
	}
}
