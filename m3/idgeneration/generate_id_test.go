package idgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateID(t *testing.T) {
	assert.Equal(t, "test.name", Get("test.name", nil))
	assert.Equal(t, "test.name", Get("test.name", map[string]string{}))
	assert.Equal(t, "name1+T1=v1", Get("name1", map[string]string{"T1": "v1"}))
	assert.Equal(t, "name1+t1=v1,t2=v2", Get("name1", map[string]string{"t2": "v2", "t1": "v1"}))
	assert.Equal(t, "name1+t1=,t2=v2", Get("name1", map[string]string{"t2": "v2", "t1": ""}))
}
