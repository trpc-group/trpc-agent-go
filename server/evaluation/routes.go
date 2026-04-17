//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import "net/http"

// RouteRegistrar registers extra routes onto the evaluation server mux.
type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux, server *Server) error
}
