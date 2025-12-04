//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewToolSet_Default(t *testing.T) {
	set, err := NewToolSet()
	assert.NoError(t, err)
	ets := set.(*emailToolSet)
	assert.Equal(t, true, ets.sendEmailEnabled)
}

// 由CodeBuddy（内网版）生成于2025.11.27 09:52:42
func Test_emailToolSet_Close(t *testing.T) {
	type fields struct {
		sendEmailEnabled bool
		tools            []tool.Tool
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name:    "zero value",
			fields:  fields{},
			wantErr: false,
		},
		{
			name: "enabled with tools",
			fields: fields{
				sendEmailEnabled: true,
				tools:            []tool.Tool{},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &emailToolSet{
				sendEmailEnabled: tt.fields.sendEmailEnabled,
				tools:            tt.fields.tools,
			}
			err := e.Close()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// 由CodeBuddy（内网版）生成于2025.11.27 09:52:43
func Test_emailToolSet_Name(t *testing.T) {
	tests := []struct {
		name   string
		fields struct {
			sendEmailEnabled bool
			tools            []tool.Tool
		}
		want string
	}{
		{
			name: "default zero value",
			fields: struct {
				sendEmailEnabled bool
				tools            []tool.Tool
			}{},
			want: "email",
		},
		{
			name: "non-zero fields",
			fields: struct {
				sendEmailEnabled bool
				tools            []tool.Tool
			}{
				sendEmailEnabled: true,
				tools:            []tool.Tool{},
			},
			want: "email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &emailToolSet{
				sendEmailEnabled: tt.fields.sendEmailEnabled,
				tools:            tt.fields.tools,
			}
			assert.Equal(t, tt.want, e.Name())
		})
	}
}

// 由CodeBuddy（内网版）生成于2025.11.27 09:52:44
func TestMailboxTypeToString(t *testing.T) {
	type args struct {
		mailboxType MailboxType
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"qq", args{mailboxType: MailQQ}, "qq"},
		{"163", args{mailboxType: Mail163}, "163"},
		{"gmail", args{mailboxType: MailGmail}, "gmail"},
		{"zero", args{mailboxType: 0}, "unknown"},
		{"negative", args{mailboxType: -1}, "unknown"},
		{"undefined", args{mailboxType: 99}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MailboxTypeToString(tt.args.mailboxType); got != tt.want {
				t.Errorf("MailboxTypeToString() = %v, want %v", got, tt.want)
			}
		})
	}
}
