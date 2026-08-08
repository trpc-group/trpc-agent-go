//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build windows

package safety

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openAuditFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	securityAttributes, err := currentUserSecurityAttributes()
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_APPEND_DATA|windows.FILE_WRITE_ATTRIBUTES|
			windows.READ_CONTROL|windows.WRITE_DAC|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ,
		securityAttributes,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("create audit file handle")
	}
	if err := validateWindowsAuditHandle(handle); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func currentUserSecurityAttributes() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get current Windows user: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;GA;;;" + user.User.Sid.String() + ")",
	)
	if err != nil {
		return nil, fmt.Errorf("create audit file security descriptor: %w", err)
	}
	attributes := &windows.SecurityAttributes{
		SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	return attributes, nil
}

func validateWindowsAuditHandle(handle windows.Handle) error {
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_DISK {
		return errors.New("audit path is not a disk file")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("audit path is a reparse point")
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return errors.New("audit path is not a regular file")
	}
	return nil
}

func setAuditFilePermissions(file *os.File) error {
	attributes, err := currentUserSecurityAttributes()
	if err != nil {
		return err
	}
	dacl, _, err := attributes.SecurityDescriptor.DACL()
	if err != nil {
		return fmt.Errorf("read audit file DACL: %w", err)
	}
	return windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}
