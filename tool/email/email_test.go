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

func TestWithName(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name  string
		args  args
		check func(*emailToolSet)
	}{
		{
			name: "normal name",
			args: args{name: "aFrqvjeXQ"},
			check: func(e *emailToolSet) {
				assert.Equal(t, "aFrqvjeXQ", e.name)
			},
		},
		{
			name: "empty name",
			args: args{name: ""},
			check: func(e *emailToolSet) {
				assert.Equal(t, "", e.name)
			},
		},
		{
			name: "unicode name",
			args: args{name: "测试邮箱"},
			check: func(e *emailToolSet) {
				assert.Equal(t, "测试邮箱", e.name)
			},
		},
		{
			name: "very long name",
			args: args{name: string(make([]byte, 1024))},
			check: func(e *emailToolSet) {
				assert.Len(t, e.name, 1024)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &emailToolSet{}
			opt := WithName(tt.args.name)
			opt(e)
			tt.check(e)
		})
	}
}

func TestNewToolSet_Default(t *testing.T) {
	set, err := NewToolSet()
	assert.NoError(t, err)
	ets := set.(*emailToolSet)
	assert.Equal(t, true, ets.sendEmailEnabled)
	assert.Equal(t, defaultName, ets.name)
}
