package apiclient_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
)

func TestReadFileBase64(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cuerpo.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"hola":"mundo"}`), 0o600))

	read, err := apiclient.ReadFileBase64(path)
	require.NoError(t, err)

	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(`{"hola":"mundo"}`)), read.Base64)
	assert.Equal(t, "application/json", read.Mime)
	assert.Equal(t, int64(16), read.Size)
}

func TestReadFileBase64OnBinaryContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captura.png")
	content := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}
	require.NoError(t, os.WriteFile(path, content, 0o600))

	read, err := apiclient.ReadFileBase64(path)
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(read.Base64)
	require.NoError(t, err)
	assert.Equal(t, content, decoded, "the bytes survive the round trip exactly")
	assert.Equal(t, "image/png", read.Mime)
}

func TestMimeOf(t *testing.T) {
	tests := map[string]string{
		"cuerpo.json":     "application/json",
		"datos.CSV":       "text/csv",
		"captura.png":     "image/png",
		"esquema.graphql": "application/graphql",
		"cliente.pem":     "application/x-pem-file",
		"identidad.p12":   "application/x-pkcs12",
		"informe.pdf":     "application/pdf",
		// Not guessed from the content, and not looked up in the host's registry: what the server
		// should be told is what the user thinks they are sending, which is what the extension says.
		"sin-extension":    "application/octet-stream",
		"algo.desconocido": "application/octet-stream",
		"archivo.tar.gz":   "application/gzip",
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, expected, apiclient.MimeOf(name))
		})
	}
}

func TestReadTextFileRefusesWhatIsNotUTF8(t *testing.T) {
	directory := t.TempDir()

	valid := filepath.Join(directory, "coleccion.json")
	require.NoError(t, os.WriteFile(valid, []byte(`{"nombre":"Facturación"}`), 0o600))

	read, err := apiclient.ReadTextFile(valid)
	require.NoError(t, err)
	assert.Equal(t, `{"nombre":"Facturación"}`, read)

	// Refusing is the point: the callers are a collection import and the runner's data file, and
	// both would otherwise proceed with U+FFFD where a byte used to be — a parse that half-succeeds
	// and produces a collection with mangled names.
	invalid := filepath.Join(directory, "latin1.csv")
	require.NoError(t, os.WriteFile(invalid, []byte{'F', 'a', 'c', 't', 'u', 'r', 0xf3, 'n'}, 0o600))

	_, err = apiclient.ReadTextFile(invalid)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid UTF-8")
	assert.Contains(t, err.Error(), "latin1.csv", "the message names the file the user picked")
}

func TestReadingAFileThatIsNotThereNamesIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-existe.json")

	_, err := apiclient.ReadFileBase64(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-existe.json")

	_, err = apiclient.ReadTextFile(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-existe.json")
}
