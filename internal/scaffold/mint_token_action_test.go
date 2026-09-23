package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMintTokenActionSurfaces422Body guards the GHA mint-token wrapper
// against regressing to curl -f, which discards the mint error body on
// 4xx. The mint already returns a specific 422 diagnostic; the action
// must print it (and the selected-repo hint) instead of curl's generic
// "returned error: 422".
func TestMintTokenActionSurfaces422Body(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", ".github/actions/mint-token/action.yml"))
	require.NoError(t, err)
	s := string(content)

	idx := strings.Index(s, `echo "Requesting token:`)
	require.Greater(t, idx, 0, "mint request log line not found")
	mintBlock := s[idx:]

	assert.NotContains(t, mintBlock, "curl -sSf",
		"mint POST must not use curl -f; that discards the HTTP body on 4xx")
	assert.Contains(t, mintBlock, `-w "%{http_code}"`)
	assert.Contains(t, mintBlock, `::error::Token mint returned HTTP`)
	assert.Contains(t, mintBlock,
		"A 422 error code usually means that the GitHub application (fullsend-ai-${ROLE}) is not installed for the repository. Check if it is installed in certain selected repositories.")
	assert.Contains(t, mintBlock, "408|429|5??|000")
}
