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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	gomail "github.com/wneessen/go-mail"
)

func Test_emailToolSet_sendMail(t *testing.T) {
	toolSet, err := NewToolSet()
	if err != nil {
		t.Errorf("NewToolSet failed, err: %v", err)
	}

	tests := []struct {
		Name     string
		Password string
		ToEmail  string
		Subject  string
		Content  string
		wantErr  bool
	}{
		// qq to gmail
		{
			Name:     "1850396756@qq.com",
			Password: "",
			ToEmail:  "zhuangguang5524621@gmail.com",
			Subject:  "test",
			Content:  "test",
			wantErr:  false,
		},
		// gmail to qq
		{
			Name:     "zhuangguang5524621@gmail.com",
			Password: "",
			ToEmail:  "1850396756@qq.com",
			Subject:  "test",
			Content:  "test",
			wantErr:  false,
		},
		// 163 to gmail
		{
			Name:     "18218025138@163.com",
			Password: "",
			ToEmail:  "zhuangguang5524621@gmail.com",
			Subject:  "test",
			Content:  "test",
			wantErr:  false,
		},
	}
	for _, tt := range tests {

		rsp, err := toolSet.(*emailToolSet).sendMail(context.Background(), &sendMailRequest{
			Auth: Auth{
				Name:     tt.Name,
				Password: tt.Password,
			},
			MailList: []*Mail{
				{
					ToEmail: tt.ToEmail,
					Subject: tt.Subject,
					Content: tt.Content,
				},
			},
		})
		t.Logf("rsp: %+v err:%v", rsp, err)
		if tt.Password == "" {
			t.Skip("password is empty, skip")
		}
		if rsp.Message != "" {
			if tt.wantErr == false {
				t.Errorf("send mail err: %s", rsp.Message)
			}
		} else if tt.wantErr == true {
			t.Errorf("should err but not")
		}

	}
}

func Test_emailToolSet_sendMail2(t *testing.T) {
	toolSet, err := NewToolSet()
	if err != nil {
		t.Errorf("NewToolSet failed, err: %v", err)
	}

	tests := []struct {
		Name     string
		Password string
		ToEmail  string
		Subject  string
		Content  string
		wantErr  bool
	}{
		// error case
		{
			Name:     "18503@96756@qq.com",
			Password: "",
			ToEmail:  "zhuangguang5524621@gmail.com",
			Subject:  "test",
			Content:  "test",
			wantErr:  true,
		},
		// error case
		{
			Name:     "zhuangguang5524621@gmail.com",
			Password: "",
			ToEmail:  "185039@6756@qq.com",
			Subject:  "test",
			Content:  "test",
			wantErr:  true,
		},
	}
	for _, tt := range tests {

		rsp, err := toolSet.(*emailToolSet).sendMail(context.Background(), &sendMailRequest{
			Auth: Auth{
				Name:     tt.Name,
				Password: tt.Password,
			},
			MailList: []*Mail{
				{
					ToEmail: tt.ToEmail,
					Subject: tt.Subject,
					Content: tt.Content,
				},
			},
		})
		t.Logf("rsp: %+v err:%v", rsp, err)
		if rsp.Message != "" {
			if tt.wantErr == false {
				t.Errorf("send mail err: %s", rsp.Message)
			}
		} else if tt.wantErr == true {
			t.Errorf("should err but not")
		}

	}
}

func Test_checkMailBoxType(t *testing.T) {
	type args struct {
		email string
	}
	tests := []struct {
		name    string
		args    args
		want    MailboxType
		wantErr bool
	}{
		{
			name:    "QQ domain",
			args:    args{email: "user@qq.com"},
			want:    MailQQ,
			wantErr: false,
		},
		{
			name:    "QQ vip domain",
			args:    args{email: "user@vip.qq.com"},
			want:    MailQQ,
			wantErr: false,
		},
		{
			name:    "Foxmail domain",
			args:    args{email: "user@foxmail.com"},
			want:    MailQQ,
			wantErr: false,
		},
		{
			name:    "Gmail domain",
			args:    args{email: "user@gmail.com"},
			want:    MailGmail,
			wantErr: false,
		},
		{
			name:    "Googlemail domain",
			args:    args{email: "user@googlemail.com"},
			want:    MailGmail,
			wantErr: false,
		},
		{
			name:    "163 domain",
			args:    args{email: "user@163.com"},
			want:    Mail163,
			wantErr: false,
		},
		{
			name:    "Unknown domain",
			args:    args{email: "user@example.com"},
			want:    MailUnknown,
			wantErr: false,
		},
		{
			name:    "Empty email",
			args:    args{email: ""},
			want:    MailUnknown,
			wantErr: true,
		},
		{
			name:    "No @ symbol",
			args:    args{email: "invalid-email"},
			want:    MailUnknown,
			wantErr: true,
		},
		{
			name:    "Multiple @ symbols",
			args:    args{email: "user@@example.com"},
			want:    MailUnknown,
			wantErr: true,
		},
		{
			name:    "Uppercase QQ domain",
			args:    args{email: "USER@QQ.COM"},
			want:    MailQQ,
			wantErr: false,
		},
		{
			name:    "Mixed case Gmail",
			args:    args{email: "User@GmAiL.cOm"},
			want:    MailGmail,
			wantErr: false,
		},
		{
			name:    "not valid email",
			args:    args{email: "UserGmAiL.cOm"},
			want:    MailUnknown,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkMailBoxType(tt.args.email)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkMailBoxType() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("checkMailBoxType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_emailToolSet_sendMailTool(t *testing.T) {
	e := &emailToolSet{}

	got := e.sendMailTool()
	assert.NotNil(t, got)

	decl := got.Declaration()
	assert.Equal(t, "send_email", decl.Name)
	assert.Equal(t, "send mail to other", decl.Description)
}

func Test_emailToolSet_getEmailAddr(t *testing.T) {
	type args struct {
		req *sendMailRequest
	}
	tests := []struct {
		name      string
		e         *emailToolSet
		args      args
		wantAddr  string
		wantPort  int
		wantIsSSL bool
		wantErr   bool
	}{
		{
			name: "custom server",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "user@example.com"},
					Extra: ExtraData{
						SvrAddr: "smtp.example.com",
						Port:    2525,
					},
				},
			},
			wantAddr:  "smtp.example.com",
			wantPort:  2525,
			wantIsSSL: false,
			wantErr:   false,
		},
		{
			name: "qq mail",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "123@qq.com"},
				},
			},
			wantAddr:  qqMail,
			wantPort:  qqPort,
			wantIsSSL: true,
			wantErr:   false,
		},
		{
			name: "gmail",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "abc@gmail.com"},
				},
			},
			wantAddr:  gmailMail,
			wantPort:  gmailPort,
			wantIsSSL: false,
			wantErr:   false,
		},
		{
			name: "163 mail",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "user@163.com"},
				},
			},
			wantAddr:  netEase163Mail,
			wantPort:  netEase1163Port,
			wantIsSSL: true,
			wantErr:   false,
		},
		{
			name: "invalid email format",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "not-an-email"},
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported domain",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "user@icloud.com"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty auth name",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "zero port with custom server",
			e:    &emailToolSet{},
			args: args{
				req: &sendMailRequest{
					Auth: Auth{Name: "user@example.com"},
					Extra: ExtraData{
						SvrAddr: "smtp.example.com",
						Port:    0,
					},
				},
			},
			wantAddr:  "smtp.example.com",
			wantPort:  0,
			wantIsSSL: false,
			wantErr:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddr, gotPort, gotIsSSL, err := tt.e.getEmailAddr(tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("getEmailAddr() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotAddr != tt.wantAddr {
				t.Errorf("getEmailAddr() gotAddr = %v, want %v", gotAddr, tt.wantAddr)
			}
			if gotPort != tt.wantPort {
				t.Errorf("getEmailAddr() gotPort = %v, want %v", gotPort, tt.wantPort)
			}
			if gotIsSSL != tt.wantIsSSL {
				t.Errorf("getEmailAddr() gotIsSSL = %v, want %v", gotIsSSL, tt.wantIsSSL)
			}
		})
	}
}

func Test_qqHandleError(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "nil error",
			args: args{
				err: nil,
			},
			wantErr: false,
		},
		{
			name: "smtp reset send error is not error",
			args: args{
				err: &gomail.SendError{
					Reason: gomail.ErrSMTPReset,
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := qqHandleError(tt.args.err); (err != nil) != tt.wantErr {
				t.Errorf("qqHandleError() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
