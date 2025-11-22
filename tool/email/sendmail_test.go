package email

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMailTool_sendMail(t *testing.T) {
	toolSet, err := NewToolSet(
		WithSendEmailEnabled(true),
		WithName("email"),
	)
	if err != nil {
		t.Errorf("NewToolSet failed, err: %v", err)
	}

	tests := []struct {
		Name     string
		Password string
		ToEmail  string
		Subject  string
		Content  string
	}{

		{
			Name:     "1850396756@qq.com",
			Password: "",
			ToEmail:  "zhuangguang5524621@gmail.com",
			Subject:  "test",
			Content:  "test",
		},

		{
			Name:     "zhuangguang5524621@gmail.com",
			Password: "",
			ToEmail:  "1850396756@qq.com",
			Subject:  "test",
			Content:  "test",
		},
	}
	for _, tt := range tests {

		if tt.Password == "" {
			t.Skip("no passwd skip")
		}

		rsp, err := toolSet.(*emailToolSet).sendMail(context.Background(), &sendMailRequest{
			Auth: Auth{
				Name:     tt.Name,
				Password: tt.Password,
			},
			MailList: []*mail{
				{
					ToEmail: tt.ToEmail,
					Subject: tt.Subject,
					Content: tt.Content,
				},
			},
		})
		if err != nil {
			t.Errorf("send mail err: %v", err)
		}
		t.Logf("rsp: %+v", rsp)
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
			want:    MAIL_QQ,
			wantErr: false,
		},
		{
			name:    "QQ vip domain",
			args:    args{email: "user@vip.qq.com"},
			want:    MAIL_QQ,
			wantErr: false,
		},
		{
			name:    "Foxmail domain",
			args:    args{email: "user@foxmail.com"},
			want:    MAIL_QQ,
			wantErr: false,
		},
		{
			name:    "Gmail domain",
			args:    args{email: "user@gmail.com"},
			want:    MAIL_GMAIL,
			wantErr: false,
		},
		{
			name:    "Googlemail domain",
			args:    args{email: "user@googlemail.com"},
			want:    MAIL_GMAIL,
			wantErr: false,
		},
		{
			name:    "163 domain",
			args:    args{email: "user@163.com"},
			want:    MAIL_163,
			wantErr: false,
		},
		{
			name:    "Unknown domain",
			args:    args{email: "user@example.com"},
			want:    MAIL_UNKNOWN,
			wantErr: false,
		},
		{
			name:    "Empty email",
			args:    args{email: ""},
			want:    MAIL_UNKNOWN,
			wantErr: true,
		},
		{
			name:    "No @ symbol",
			args:    args{email: "invalid-email"},
			want:    MAIL_UNKNOWN,
			wantErr: true,
		},
		{
			name:    "Multiple @ symbols",
			args:    args{email: "user@@example.com"},
			want:    MAIL_UNKNOWN,
			wantErr: true,
		},
		{
			name:    "Uppercase QQ domain",
			args:    args{email: "USER@QQ.COM"},
			want:    MAIL_QQ,
			wantErr: false,
		},
		{
			name:    "Mixed case Gmail",
			args:    args{email: "User@GmAiL.cOm"},
			want:    MAIL_GMAIL,
			wantErr: false,
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
