package email

import (
	"context"
	"testing"
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
		// qq to gmail
		{
			Name:     "1850396756@qq.com",
			Password: "",
			ToEmail:  "zhuangguang5524621@gmail.com",
			Subject:  "test",
			Content:  "test",
		},
		// gmail to qq
		{
			Name:     "zhuangguang5524621@gmail.com",
			Password: "",
			ToEmail:  "1850396756@qq.com",
			Subject:  "test",
			Content:  "test",
		},
	}
	for _, tt := range tests {

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
			t.Errorf("send mail failed, err: %v", err)
		}
		t.Logf("rsp: %+v", rsp)
	}
}
