//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import "github.com/redis/go-redis/v9"

// luaSummariesSetIfNewer atomically merges one filterKey summary into the stored
// JSON map only if the incoming UpdatedAt is newer-or-equal.
//
// KEYS[1] = summary key
// ARGV[1] = hash field
// ARGV[2] = filterKey
// ARGV[3] = newSummaryJSON -> {"summary":"...","updated_at":"RFC3339 time"}
//
// Returns 1 if updated, 0 if skipped (existing is newer).
var luaSummariesSetIfNewer = redis.NewScript(
	"local cur = redis.call('HGET', KEYS[1], ARGV[1])\n" +
		"local fk = ARGV[2]\n" +
		"local newSum = cjson.decode(ARGV[3])\n" +
		"if not cur or cur == '' then\n" +
		"  local m = {}\n" +
		"  m[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(m))\n" +
		"  return 1\n" +
		"end\n" +
		"local map = cjson.decode(cur)\n" +
		"local old = map[fk]\n" +
		"local old_ts = nil\n" +
		"local new_ts = nil\n" +
		"if old and old['updated_at'] then old_ts = old['updated_at'] end\n" +
		"if newSum and newSum['updated_at'] then new_ts = newSum['updated_at'] end\n" +
		"if not old or (old_ts and new_ts and old_ts <= new_ts) then\n" +
		"  map[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(map))\n" +
		"  return 1\n" +
		"end\n" +
		"return 0\n",
)
