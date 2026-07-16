//go:build windows

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openSecureAuditFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	securityDescriptor, acl, err := ownerOnlySecurityDescriptor()
	if err != nil {
		return nil, fmt.Errorf("build owner-only audit ACL: %w", err)
	}
	securityAttributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: securityDescriptor,
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC,
		windows.FILE_SHARE_READ,
		securityAttributes,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	runtime.KeepAlive(securityDescriptor)
	runtime.KeepAlive(acl)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("create audit file handle")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened audit file: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, errors.New("audit path cannot be a reparse point")
	}
	if err := windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure audit file ACL: %w", err)
	}
	if err := validateSecureAuditFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func ownerOnlySecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, *windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return nil, nil, err
	}
	securityDescriptor, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, nil, err
	}
	if err := securityDescriptor.SetDACL(acl, true, false); err != nil {
		return nil, nil, err
	}
	return securityDescriptor, acl, nil
}

func validateSecureAuditFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened audit file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("audit path must be a regular file")
	}
	return nil
}
