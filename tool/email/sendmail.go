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
	"errors"
	"fmt"
	"net/mail"
	"strings"

	gomail "github.com/wneessen/go-mail"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	qqMail          = "smtp.qq.com"
	qqPort          = 465
	gmailMail       = "smtp.gmail.com"
	gmailPort       = 587
	netEase163Mail  = "smtp.163.com"
	netEase1163Port = 465
)

// sendMailRequest represents the input for the send mail operation.
type sendMailRequest struct {
	Auth     Auth      `json:"auth" jsonschema:"description=auth of the mail."`
	MailList []*Mail   `json:"mail_list" jsonschema:"description=The list of mail."`
	Extra    ExtraData `json:"extra" jsonschema:"description=extra data of the mail. optional. default is empty."`
}

// Mail represents a mail to be sent.
type Mail struct {
	ToEmail string `json:"to_email" jsonschema:"description=send to email."`
	Subject string `json:"subject" jsonschema:"description=subject of the mail"`
	Content string `json:"content" jsonschema:"description=content of the mail"`
}

// Auth is a struct for email authentication.
type Auth struct {
	Name     string `json:"name" jsonschema:"description=name of the mail."`
	Password string `json:"password" jsonschema:"description=password of the mail."`
}

// ExtraData represents extra data for the mail.
type ExtraData struct {
	SvrAddr string `json:"svr_addr" jsonschema:"description=server address of the mail. optional. default is empty."`
	Port    int    `json:"port" jsonschema:"description=port of the mail. optional. default is empty."`
}

// sendMailResponse represents the output from the send mail operation.
type sendMailResponse struct {
	Message string `json:"message"`
}

// sendMail performs the send mail operation.
func (e *emailToolSet) sendMail(ctx context.Context, req *sendMailRequest) (rsp *sendMailResponse, err error) {
	rsp = &sendMailResponse{}

	addr, port, isSSL, err := e.getEmailAddr(req)
	if err != nil {
		rsp.Message = fmt.Sprintf("getSvrAddrAndPort ERROR: %v", err)
		return
	}

	opts := []gomail.Option{
		gomail.WithPort(port),
		gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
		gomail.WithUsername(req.Auth.Name),
		gomail.WithPassword(req.Auth.Password),
		gomail.WithoutNoop(),
		gomail.WithDebugLog(),
	}
	if isSSL {
		opts = append(opts, gomail.WithSSL())
	} else {
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}

	client, err := gomail.NewClient(
		addr,
		opts...,
	)
	if err != nil {
		rsp.Message = fmt.Sprintf("the server address err: %v", err)
		return rsp, nil
	}

	defer func() {
		_ = client.Close()
	}()

	messages := make([]*gomail.Msg, 0, len(req.MailList))
	for _, m := range req.MailList {
		message := gomail.NewMsg()
		err = message.From(req.Auth.Name)
		if err != nil {
			rsp.Message = fmt.Sprintf("from email err: %v", err)
			return rsp, nil
		}
		err = message.To(m.ToEmail)
		if err != nil {
			rsp.Message = fmt.Sprintf("to email err: %v", err)
			return rsp, nil
		}
		message.Subject(m.Subject)
		message.SetBodyString(gomail.TypeTextHTML, m.Content)
		messages = append(messages, message)
	}

	// batch send email, not stop if one failed, return err  which join all send error message
	if err := client.DialAndSendWithContext(ctx, messages...); err != nil {
		//qq mail special error handle
		//https://github.com/wneessen/go-mail/issues/463
		if addr == qqMail && qqHandleError(err) == nil {

		} else {
			rsp.Message = fmt.Sprintf("send ERROR: %v, host:%s, port:%d", err, addr, port)
			return rsp, nil
		}
	}

	return
}

// getEmailAddr gets the email address and port.
func (e *emailToolSet) getEmailAddr(req *sendMailRequest) (addr string, port int, isSSL bool, err error) {
	mailBoxType, err := checkMailBoxType(req.Auth.Name)
	if err != nil {
		err = fmt.Errorf("checkMailBoxType ERROR: %v addr:%s", err, req.Auth.Name)
		return
	}

	if req.Extra.SvrAddr != "" {
		addr = req.Extra.SvrAddr
		port = req.Extra.Port
	} else {
		switch mailBoxType {
		case MailQQ:
			//qq email
			addr = qqMail
			port = qqPort
			isSSL = true
		case MailGmail:
			//gmail email
			addr = gmailMail
			port = gmailPort
			isSSL = false
		case Mail163:
			//163 email
			addr = netEase163Mail
			port = netEase1163Port
			isSSL = true
		default:
			// not support
			err = fmt.Errorf("not support mailbox type:%s", MailboxTypeToString(mailBoxType))
			return
		}
	}
	return
}

// checkMailBoxType checks the mailbox type.
func checkMailBoxType(email string) (MailboxType, error) {

	addr, err := mail.ParseAddress(email)
	if err != nil {
		return MailUnknown, fmt.Errorf("parse email address ERROR: %w", err)
	}
	// to lower
	emailAddr := strings.ToLower(addr.Address)

	// split by name and domain
	lastAt := strings.LastIndex(emailAddr, "@")
	if lastAt < 0 {
		return MailUnknown, fmt.Errorf("invalid email address")
	}
	domain := emailAddr[lastAt:]
	domain = strings.TrimPrefix(domain, "@")

	switch domain {
	case "qq.com", "vip.qq.com", "foxmail.com":
		return MailQQ, nil
	case "gmail.com", "googlemail.com":
		return MailGmail, nil
	case "163.com":
		return Mail163, nil
	default:
		return MailUnknown, nil
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

func qqHandleError(err error) error {
	//log.Infof("err: %v %T", err, err)

	var sendErr *gomail.SendError
	// Check if this is an SMTP RESET error after successful delivery
	if errors.As(err, &sendErr) {
		if sendErr.Reason == gomail.ErrSMTPReset {
			// https://github.com/wneessen/go-mail/issues/463
			log.Warnf("⚠️ Mail delivered successfully but SMTP RESET failed: %s", err)
			return nil // Don't treat this as a delivery failure since mail was sent
		}
		return err
	}
	return err
}
