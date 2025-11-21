package email

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/gomail.v2"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// sendMailRequest represents the input for the send mail operation.
type sendMailRequest struct {
	Auth     Auth    `json:"auth" jsonschema:"description=auth of the mail."`
	MailList []*mail `json:"mail_list" jsonschema:"description=The list of mail."`
}

type mail struct {
	ToEmail string `json:"to_email" jsonschema:"description=send to email."`
	Subject string `json:"subject" jsonschema:"description=subject of the mail"`
	Content string `json:"content" jsonschema:"description=content of the mail"`
}

// Auth is a struct for email authentication.
type Auth struct {
	Name     string `json:"name" jsonschema:"description=name of the mail."`
	Password string `json:"password" jsonschema:"description=password of the mail."`
}

// sendMailResponse represents the output from the send mail operation.
type sendMailResponse struct {
	Message string `json:"message"`
}

// sendMail performs the send mail operation.
// go smtp not support context, one send one mail, can't stop
func (e *emailToolSet) sendMail(_ context.Context, req *sendMailRequest) (rsp *sendMailResponse, err error) {
	rsp = &sendMailResponse{}

	mailBoxType, err := checkMailBoxType(req.Auth.Name)
	if err != nil {
		rsp.Message = fmt.Sprintf("checkMailBoxType ERROR: %v", err)
		return rsp, nil
	}

	var addr string
	var port int
	switch mailBoxType {
	case MAIL_QQ:
		//qq email
		addr = "smtp.qq.com"
		port = 465
	case MAIL_GMAIL:
		//gmail email
		addr = "smtp.gmail.com"
		port = 587
	default:
		// not support
		rsp.Message = fmt.Sprintf("not support mailbox type:%s", MailboxTypeToString(mailBoxType))
		return rsp, nil
	}

	dialer := gomail.NewDialer(addr, port, req.Auth.Name, req.Auth.Password)
	s, err := dialer.Dial()
	if err != nil {
		rsp.Message = fmt.Sprintf("the address or password is incorrect,please check: %v", err)
		return rsp, nil
	}
	defer func() {
		_ = s.Close()
	}()

	message := gomail.NewMessage()
	for _, m := range req.MailList {
		message.SetHeader("From", req.Auth.Name)
		message.SetHeader("To", m.ToEmail)
		message.SetHeader("Subject", m.Subject)
		message.SetBody("text/html", m.Content)
		if err := gomail.Send(s, message); err != nil {
			rsp.Message = fmt.Sprintf("send ERROR: %v", err)
			return rsp, fmt.Errorf("send ERROR: %w", err)
		}
		message.Reset()
	}

	return
}

// checkMailBoxType checks the mailbox type.
func checkMailBoxType(email string) (MailboxType, error) {
	// to lower
	email = strings.ToLower(email)

	// split by name and domain
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return MAIL_UNKNOWN, fmt.Errorf("invalid email address")
	}

	domain := parts[1]

	switch domain {
	case "qq.com", "vip.qq.com", "foxmail.com":
		return MAIL_QQ, nil
	case "gmail.com", "googlemail.com":
		return MAIL_GMAIL, nil
	case "163.com":
		return MAIL_163, nil
	default:
		return MAIL_UNKNOWN, nil
	}
}

// sendMailTool returns a callable tool for send mail.
func (e *emailToolSet) sendMailTool() tool.CallableTool {
	return function.NewFunctionTool(
		e.sendMail,
		function.WithName("send_email"),
		function.WithDescription("send mail to other"),
	)
}
