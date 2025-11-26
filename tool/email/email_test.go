package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithSendEmailEnabled(t *testing.T) {
	t.Run("set true", func(t *testing.T) {
		e := &emailToolSet{}
		opt := WithSendEmailEnabled(true)
		opt(e)
		assert.True(t, e.sendEmailEnabled)
	})

	t.Run("set false", func(t *testing.T) {
		e := &emailToolSet{}
		opt := WithSendEmailEnabled(false)
		opt(e)
		assert.False(t, e.sendEmailEnabled)
	})
}

func TestNewToolSet_Default(t *testing.T) {
	set, err := NewToolSet()
	assert.NoError(t, err)
	ets := set.(*emailToolSet)
	assert.Equal(t, true, ets.sendEmailEnabled)
}
