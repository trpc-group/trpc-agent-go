//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRegularRepairer_Repair_MatchesCases verifies repaired output matches expected results.
func TestRegularRepairer_Repair_MatchesCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "{\"a\":2.3e100,\"b\":\"str\",\"c\":null,\"d\":false,\"e\":[1,2,3]}", want: "{\"a\":2.3e100,\"b\":\"str\",\"c\":null,\"d\":false,\"e\":[1,2,3]}"},
		{input: "  { \n } \t ", want: "  { \n } \t "},
		{input: "{}", want: "{}"},
		{input: "{  }", want: "{  }"},
		{input: "{\"a\": {}}", want: "{\"a\": {}}"},
		{input: "{\"a\": \"b\"}", want: "{\"a\": \"b\"}"},
		{input: "{\"a\": 2}", want: "{\"a\": 2}"},
		{input: "[]", want: "[]"},
		{input: "[  ]", want: "[  ]"},
		{input: "[1,2,3]", want: "[1,2,3]"},
		{input: "[ 1 , 2 , 3 ]", want: "[ 1 , 2 , 3 ]"},
		{input: "[1,2,[3,4,5]]", want: "[1,2,[3,4,5]]"},
		{input: "[{}]", want: "[{}]"},
		{input: "{\"a\":[]}", want: "{\"a\":[]}"},
		{input: "[1, \"hi\", true, false, null, {}, []]", want: "[1, \"hi\", true, false, null, {}, []]"},
		{input: "23", want: "23"},
		{input: "0", want: "0"},
		{input: "0e+2", want: "0e+2"},
		{input: "0.0", want: "0.0"},
		{input: "-0", want: "-0"},
		{input: "2.3", want: "2.3"},
		{input: "2300e3", want: "2300e3"},
		{input: "2300e+3", want: "2300e+3"},
		{input: "2300e-3", want: "2300e-3"},
		{input: "-2", want: "-2"},
		{input: "2e-3", want: "2e-3"},
		{input: "2.3e-3", want: "2.3e-3"},
		{input: "\"str\"", want: "\"str\""},
		{input: "\"\\\"\\\\\\/\\b\\f\\n\\r\\t\"", want: "\"\\\"\\\\\\/\\b\\f\\n\\r\\t\""},
		{input: "\"\\u260E\"", want: "\"\\u260E\""},
		{input: "true", want: "true"},
		{input: "false", want: "false"},
		{input: "null", want: "null"},
		{input: "\"\"", want: "\"\""},
		{input: "\"[\"", want: "\"[\""},
		{input: "\"]\"", want: "\"]\""},
		{input: "\"{\"", want: "\"{\""},
		{input: "\"}\"", want: "\"}\""},
		{input: "\":\"", want: "\":\""},
		{input: "\",\"", want: "\",\""},
		{input: "\"\u2605\"", want: "\"\u2605\""},
		{input: "\"\U0001f600\"", want: "\"\U0001f600\""},
		{input: "\"\u0439\u043d\u0444\u043e\u0440\u043c\u0430\u0446\u0438\u044f\"", want: "\"\u0439\u043d\u0444\u043e\u0440\u043c\u0430\u0446\u0438\u044f\""},
		{input: "\"\\u2605\"", want: "\"\\u2605\""},
		{input: "\"\\u2605A\"", want: "\"\\u2605A\""},
		{input: "\"\\ud83d\\ude00\"", want: "\"\\ud83d\\ude00\""},
		{input: "\"\\u0439\\u043d\\u0444\\u043e\\u0440\\u043c\\u0430\\u0446\\u0438\\u044f\"", want: "\"\\u0439\\u043d\\u0444\\u043e\\u0440\\u043c\\u0430\\u0446\\u0438\\u044f\""},
		{input: "{\"\u2605\":true}", want: "{\"\u2605\":true}"},
		{input: "{\"\\u2605\":true}", want: "{\"\\u2605\":true}"},
		{input: "{\"\U0001f600\":true}", want: "{\"\U0001f600\":true}"},
		{input: "abc", want: "\"abc\""},
		{input: "hello   world", want: "\"hello   world\""},
		{input: "{\nmessage: hello world\n}", want: "{\n\"message\": \"hello world\"\n}"},
		{input: "{a:2}", want: "{\"a\":2}"},
		{input: "{a: 2}", want: "{\"a\": 2}"},
		{input: "{2: 2}", want: "{\"2\": 2}"},
		{input: "{true: 2}", want: "{\"true\": 2}"},
		{input: "{\n  a: 2\n}", want: "{\n  \"a\": 2\n}"},
		{input: "[a,b]", want: "[\"a\",\"b\"]"},
		{input: "[\na,\nb\n]", want: "[\n\"a\",\n\"b\"\n]"},
		{input: "https://www.bible.com/", want: "\"https://www.bible.com/\""},
		{input: "{url:https://www.bible.com/}", want: "{\"url\":\"https://www.bible.com/\"}"},
		{input: "{url:https://www.bible.com/,\"id\":2}", want: "{\"url\":\"https://www.bible.com/\",\"id\":2}"},
		{input: "[https://www.bible.com/]", want: "[\"https://www.bible.com/\"]"},
		{input: "[https://www.bible.com/,2]", want: "[\"https://www.bible.com/\",2]"},
		{input: "\"https://www.bible.com/", want: "\"https://www.bible.com/\""},
		{input: "{\"url\":\"https://www.bible.com/}", want: "{\"url\":\"https://www.bible.com/\"}"},
		{input: "{\"url\":\"https://www.bible.com/,\"id\":2}", want: "{\"url\":\"https://www.bible.com/\",\"id\":2}"},
		{input: "[\"https://www.bible.com/]", want: "[\"https://www.bible.com/\"]"},
		{input: "[\"https://www.bible.com/,2]", want: "[\"https://www.bible.com/\",2]"},
		{input: "\"abc", want: "\"abc\""},
		{input: "'abc", want: "\"abc\""},
		{input: "\"12:20", want: "\"12:20\""},
		{input: "{\"time\":\"12:20}", want: "{\"time\":\"12:20\"}"},
		{input: "{\"date\":2024-10-18T18:35:22.229Z}", want: "{\"date\":\"2024-10-18T18:35:22.229Z\"}"},
		{input: "\"She said:", want: "\"She said:\""},
		{input: "{\"text\": \"She said:", want: "{\"text\": \"She said:\"}"},
		{input: "[\"hello, world]", want: "[\"hello\", \"world\"]"},
		{input: "[\"hello,\"world\"]", want: "[\"hello\",\"world\"]"},
		{input: "{\"a\":\"b}", want: "{\"a\":\"b\"}"},
		{input: "{\"a\":\"b,\"c\":\"d\"}", want: "{\"a\":\"b\",\"c\":\"d\"}"},
		{input: "{\"a\":\"b,c,\"d\":\"e\"}", want: "{\"a\":\"b,c\",\"d\":\"e\"}"},
		{input: "{a:\"b,c,\"d\":\"e\"}", want: "{\"a\":\"b,c\",\"d\":\"e\"}"},
		{input: "[\"b,c,]", want: "[\"b\",\"c\"]"},
		{input: "\u2018abc", want: "\"abc\""},
		{input: "\"it's working", want: "\"it's working\""},
		{input: "[\"abc+/*comment*/\"def\"]", want: "[\"abcdef\"]"},
		{input: "[\"abc/*comment*/+\"def\"]", want: "[\"abcdef\"]"},
		{input: "[\"abc,/*comment*/\"def\"]", want: "[\"abc\",\"def\"]"},
		{input: "\"foo", want: "\"foo\""},
		{input: "[", want: "[]"},
		{input: "[\"foo", want: "[\"foo\"]"},
		{input: "[\"foo\"", want: "[\"foo\"]"},
		{input: "[\"foo\",", want: "[\"foo\"]"},
		{input: "{\"foo\":\"bar\"", want: "{\"foo\":\"bar\"}"},
		{input: "{\"foo\":\"bar", want: "{\"foo\":\"bar\"}"},
		{input: "{\"foo\":", want: "{\"foo\":null}"},
		{input: "{\"foo\"", want: "{\"foo\":null}"},
		{input: "{\"foo", want: "{\"foo\":null}"},
		{input: "{", want: "{}"},
		{input: "2.", want: "2.0"},
		{input: "2e", want: "2e0"},
		{input: "2e+", want: "2e+0"},
		{input: "2e-", want: "2e-0"},
		{input: "{\"foo\":\"bar\\u20", want: "{\"foo\":\"bar\"}"},
		{input: "\"\\u", want: "\"\""},
		{input: "\"\\u2", want: "\"\""},
		{input: "\"\\u260", want: "\"\""},
		{input: "\"\\u2605", want: "\"\\u2605\""},
		{input: "{\"s \\ud", want: "{\"s\": null}"},
		{input: "{\"message\": \"it's working", want: "{\"message\": \"it's working\"}"},
		{input: "{\"text\":\"Hello Sergey,I hop", want: "{\"text\":\"Hello Sergey,I hop\"}"},
		{input: "{\"message\": \"with, multiple, comma's, you see?", want: "{\"message\": \"with, multiple, comma's, you see?\"}"},
		{input: "[1,2,3,...]", want: "[1,2,3]"},
		{input: "[1, 2, 3, ... ]", want: "[1, 2, 3  ]"},
		{input: "[1,2,3,/*comment1*/.../*comment2*/]", want: "[1,2,3]"},
		{input: "[\n  1,\n  2,\n  3,\n  /*comment1*/  .../*comment2*/\n]", want: "[\n  1,\n  2,\n  3\n    \n]"},
		{input: "{\"array\":[1,2,3,...]}", want: "{\"array\":[1,2,3]}"},
		{input: "[1,2,3,...,9]", want: "[1,2,3,9]"},
		{input: "[...,7,8,9]", want: "[7,8,9]"},
		{input: "[..., 7,8,9]", want: "[ 7,8,9]"},
		{input: "[...]", want: "[]"},
		{input: "[ ... ]", want: "[  ]"},
		{input: "{\"a\":2,\"b\":3,...}", want: "{\"a\":2,\"b\":3}"},
		{input: "{\"a\":2,\"b\":3,/*comment1*/.../*comment2*/}", want: "{\"a\":2,\"b\":3}"},
		{input: "{\n  \"a\":2,\n  \"b\":3,\n  /*comment1*/.../*comment2*/\n}", want: "{\n  \"a\":2,\n  \"b\":3\n  \n}"},
		{input: "{\"a\":2,\"b\":3, ... }", want: "{\"a\":2,\"b\":3  }"},
		{input: "{\"nested\":{\"a\":2,\"b\":3, ... }}", want: "{\"nested\":{\"a\":2,\"b\":3  }}"},
		{input: "{\"a\":2,\"b\":3,...,\"z\":26}", want: "{\"a\":2,\"b\":3,\"z\":26}"},
		{input: "{...}", want: "{}"},
		{input: "{ ... }", want: "{  }"},
		{input: "abc\"", want: "\"abc\""},
		{input: "[a\",\"b\"]", want: "[\"a\",\"b\"]"},
		{input: "[a\",b\"]", want: "[\"a\",\"b\"]"},
		{input: "{\"a\":\"foo\",\"b\":\"bar\"}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "{a\":\"foo\",\"b\":\"bar\"}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "{\"a\":\"foo\",b\":\"bar\"}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "{\"a\":foo\",\"b\":\"bar\"}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "[\n\"abc,\n\"def\"\n]", want: "[\n\"abc\",\n\"def\"\n]"},
		{input: "[\n\"abc,  \n\"def\"\n]", want: "[\n\"abc\",  \n\"def\"\n]"},
		{input: "[\"abc]\n", want: "[\"abc\"]\n"},
		{input: "[\"abc  ]\n", want: "[\"abc\"  ]\n"},
		{input: "[\n[\n\"abc\n]\n]\n", want: "[\n[\n\"abc\"\n]\n]\n"},
		{input: "{'a':2}", want: "{\"a\":2}"},
		{input: "{'a':'foo'}", want: "{\"a\":\"foo\"}"},
		{input: "{\"a\":'foo'}", want: "{\"a\":\"foo\"}"},
		{input: "{a:'foo',b:'bar'}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "{\u201ca\u201d:\u201cb\u201d}", want: "{\"a\":\"b\"}"},
		{input: "{\u2018a\u2019:\u2018b\u2019}", want: "{\"a\":\"b\"}"},
		{input: "{`a\u00b4:`b\u00b4}", want: "{\"a\":\"b\"}"},
		{input: "\"Rounded \u201c quote\"", want: "\"Rounded \u201c quote\""},
		{input: "'Rounded \u201c quote'", want: "\"Rounded \u201c quote\""},
		{input: "\"Rounded \u2019 quote\"", want: "\"Rounded \u2019 quote\""},
		{input: "'Rounded \u2019 quote'", want: "\"Rounded \u2019 quote\""},
		{input: "'Double \" quote'", want: "\"Double \\\" quote\""},
		{input: "{pattern: '\u2019'}", want: "{\"pattern\": \"\u2019\"}"},
		{input: "\"{a:b}\"", want: "\"{a:b}\""},
		{input: "\"foo'bar\"", want: "\"foo'bar\""},
		{input: "\"foo\\\"bar\"", want: "\"foo\\\"bar\""},
		{input: "'foo\"bar'", want: "\"foo\\\"bar\""},
		{input: "'foo\\'bar'", want: "\"foo'bar\""},
		{input: "\"foo\\'bar\"", want: "\"foo'bar\""},
		{input: "\"\\a\"", want: "\"a\""},
		{input: "{\"a\":}", want: "{\"a\":null}"},
		{input: "{\"a\":,\"b\":2}", want: "{\"a\":null,\"b\":2}"},
		{input: "{\"a\":", want: "{\"a\":null}"},
		{input: "{\"a\":undefined}", want: "{\"a\":null}"},
		{input: "[undefined]", want: "[null]"},
		{input: "undefined", want: "null"},
		{input: "\"hello\bworld\"", want: "\"hello\\bworld\""},
		{input: "\"hello\fworld\"", want: "\"hello\\fworld\""},
		{input: "\"hello\nworld\"", want: "\"hello\\nworld\""},
		{input: "\"hello\rworld\"", want: "\"hello\\rworld\""},
		{input: "\"hello\tworld\"", want: "\"hello\\tworld\""},
		{input: "{\"key\nafter\": \"foo\"}", want: "{\"key\\nafter\": \"foo\"}"},
		{input: "[\"hello\nworld\"]", want: "[\"hello\\nworld\"]"},
		{input: "[\"hello\nworld\"  ]", want: "[\"hello\\nworld\"  ]"},
		{input: "[\"hello\nworld\"\n]", want: "[\"hello\\nworld\"\n]"},
		{input: "\"The TV has a 24\" screen\"", want: "\"The TV has a 24\\\" screen\""},
		{input: "{\"key\": \"apple \"bee\" carrot\"}", want: "{\"key\": \"apple \\\"bee\\\" carrot\"}"},
		{input: "[\",\",\":\"]", want: "[\",\",\":\"]"},
		{input: "[\"a\" 2]", want: "[\"a\", 2]"},
		{input: "[\"a\" 2", want: "[\"a\", 2]"},
		{input: "[\",\" 2", want: "[\",\", 2]"},
		{input: "{\"a\":\u00a0\"foo\u00a0bar\"}", want: "{\"a\": \"foo\u00a0bar\"}"},
		{input: "{\"a\":\u202f\"foo\"}", want: "{\"a\": \"foo\"}"},
		{input: "{\"a\":\u205f\"foo\"}", want: "{\"a\": \"foo\"}"},
		{input: "{\"a\":\u3000\"foo\"}", want: "{\"a\": \"foo\"}"},
		{input: "\u2018foo\u2019", want: "\"foo\""},
		{input: "\u201cfoo\u201d", want: "\"foo\""},
		{input: "`foo\u00b4", want: "\"foo\""},
		{input: "`foo'", want: "\"foo\""},
		{input: "/* foo */ {}", want: " {}"},
		{input: "/*a*/ /*b*/ {}", want: "  {}"},
		{input: "{} /* foo */ ", want: "{}  "},
		{input: "{} /* foo ", want: "{} "},
		{input: "\n/* foo */\n{}", want: "\n\n{}"},
		{input: "{\"a\":\"foo\",/*hello*/\"b\":\"bar\"}", want: "{\"a\":\"foo\",\"b\":\"bar\"}"},
		{input: "{\"flag\":/*boolean*/true}", want: "{\"flag\":true}"},
		{input: "{} // comment", want: "{} "},
		{input: "{\n\"a\":\"foo\",//hello\n\"b\":\"bar\"\n}", want: "{\n\"a\":\"foo\",\n\"b\":\"bar\"\n}"},
		{input: "\"/* foo */\"", want: "\"/* foo */\""},
		{input: "[\"a\"/* foo */]", want: "[\"a\"]"},
		{input: "[\"(a)\"/* foo */]", want: "[\"(a)\"]"},
		{input: "[\"a]\"/* foo */]", want: "[\"a]\"]"},
		{input: "{\"a\":\"b\"/* foo */}", want: "{\"a\":\"b\"}"},
		{input: "{\"a\":\"(b)\"/* foo */}", want: "{\"a\":\"(b)\"}"},
		{input: "callback_123({});", want: "{}"},
		{input: "callback_123([]);", want: "[]"},
		{input: "callback_123(2);", want: "2"},
		{input: "callback_123(\"foo\");", want: "\"foo\""},
		{input: "callback_123(null);", want: "null"},
		{input: "callback_123(true);", want: "true"},
		{input: "callback_123(false);", want: "false"},
		{input: "callback({}", want: "{}"},
		{input: "/* foo bar */ callback_123 ({})", want: " {}"},
		{input: "/* foo bar */\ncallback_123({})", want: "\n{}"},
		{input: "/* foo bar */ callback_123 (  {}  )", want: "   {}  "},
		{input: "  /* foo bar */   callback_123({});  ", want: "     {}  "},
		{input: "\n/* foo\nbar */\ncallback_123 ({});\n\n", want: "\n\n{}\n\n"},
		{input: "```\n{\"a\":\"b\"}\n```", want: "\n{\"a\":\"b\"}\n"},
		{input: "```json\n{\"a\":\"b\"}\n```", want: "\n{\"a\":\"b\"}\n"},
		{input: "```\n{\"a\":\"b\"}\n", want: "\n{\"a\":\"b\"}\n"},
		{input: "\n{\"a\":\"b\"}\n```", want: "\n{\"a\":\"b\"}\n"},
		{input: "```{\"a\":\"b\"}```", want: "{\"a\":\"b\"}"},
		{input: "```\n[1,2,3]\n```", want: "\n[1,2,3]\n"},
		{input: "```python\n{\"a\":\"b\"}\n```", want: "\n{\"a\":\"b\"}\n"},
		{input: "\n ```json\n{\"a\":\"b\"}\n```\n  ", want: "\n \n{\"a\":\"b\"}\n\n  "},
		{input: "[```\n{\"a\":\"b\"}\n```]", want: "\n{\"a\":\"b\"}\n"},
		{input: "[```json\n{\"a\":\"b\"}\n```]", want: "\n{\"a\":\"b\"}\n"},
		{input: "{```\n{\"a\":\"b\"}\n```}", want: "\n{\"a\":\"b\"}\n"},
		{input: "{```json\n{\"a\":\"b\"}\n```}", want: "\n{\"a\":\"b\"}\n"},
		{input: "\\\"hello world\\\"", want: "\"hello world\""},
		{input: "\\\"hello world\\", want: "\"hello world\""},
		{input: "\\\"hello \\\\\"world\\\\\"\\\"", want: "\"hello \\\"world\\\"\""},
		{input: "[\\\"hello \\\\\"world\\\\\"\\\"]", want: "[\"hello \\\"world\\\"\"]"},
		{input: "{\\\"stringified\\\": \\\"hello \\\\\"world\\\\\"\\\"}", want: "{\"stringified\": \"hello \\\"world\\\"\"}"},
		{input: "[\\\"hello\\, \\\"world\\\"]", want: "[\"hello, \\\"world\"]"},
		{input: "\\\"hello\"", want: "\"hello\""},
		{input: "[,1,2,3]", want: "[1,2,3]"},
		{input: "[/* a */,/* b */1,2,3]", want: "[1,2,3]"},
		{input: "[, 1,2,3]", want: "[ 1,2,3]"},
		{input: "[ , 1,2,3]", want: "[  1,2,3]"},
		{input: "{,\"message\": \"hi\"}", want: "{\"message\": \"hi\"}"},
		{input: "{/* a */,/* b */\"message\": \"hi\"}", want: "{\"message\": \"hi\"}"},
		{input: "{ ,\"message\": \"hi\"}", want: "{ \"message\": \"hi\"}"},
		{input: "{, \"message\": \"hi\"}", want: "{ \"message\": \"hi\"}"},
		{input: "[1,2,3,]", want: "[1,2,3]"},
		{input: "[1,2,3,\n]", want: "[1,2,3\n]"},
		{input: "[1,2,3,  \n  ]", want: "[1,2,3  \n  ]"},
		{input: "[1,2,3,/*foo*/]", want: "[1,2,3]"},
		{input: "{\"array\":[1,2,3,]}", want: "{\"array\":[1,2,3]}"},
		{input: "\"[1,2,3,]\"", want: "\"[1,2,3,]\""},
		{input: "{\"a\":2,}", want: "{\"a\":2}"},
		{input: "{\"a\":2  ,  }", want: "{\"a\":2    }"},
		{input: "{\"a\":2  , \n }", want: "{\"a\":2   \n }"},
		{input: "{\"a\":2/*foo*/,/*foo*/}", want: "{\"a\":2}"},
		{input: "{},", want: "{}"},
		{input: "\"{a:2,}\"", want: "\"{a:2,}\""},
		{input: "4,", want: "4"},
		{input: "4 ,", want: "4 "},
		{input: "4 , ", want: "4  "},
		{input: "{\"a\":2},", want: "{\"a\":2}"},
		{input: "[1,2,3],", want: "[1,2,3]"},
		{input: "{\"a\":2", want: "{\"a\":2}"},
		{input: "{\"a\":2,", want: "{\"a\":2}"},
		{input: "{\"a\":{\"b\":2}", want: "{\"a\":{\"b\":2}}"},
		{input: "{\n  \"a\":{\"b\":2\n}", want: "{\n  \"a\":{\"b\":2\n}}"},
		{input: "[{\"b\":2]", want: "[{\"b\":2}]"},
		{input: "[{\"b\":2\n]", want: "[{\"b\":2}\n]"},
		{input: "[{\"i\":1{\"i\":2}]", want: "[{\"i\":1},{\"i\":2}]"},
		{input: "[{\"i\":1,{\"i\":2}]", want: "[{\"i\":1},{\"i\":2}]"},
		{input: "{\"a\": 1}}", want: "{\"a\": 1}"},
		{input: "{\"a\": 1}}]}", want: "{\"a\": 1}"},
		{input: "{\"a\": 1 }  }  ]  }  ", want: "{\"a\": 1 }        "},
		{input: "{\"a\":2]", want: "{\"a\":2}"},
		{input: "{\"a\":2,]", want: "{\"a\":2}"},
		{input: "{}}", want: "{}"},
		{input: "[2,}", want: "[2]"},
		{input: "[}", want: "[]"},
		{input: "{]", want: "{}"},
		{input: "[1,2,3", want: "[1,2,3]"},
		{input: "[1,2,3,", want: "[1,2,3]"},
		{input: "[[1,2,3,", want: "[[1,2,3]]"},
		{input: "{\n\"values\":[1,2,3\n}", want: "{\n\"values\":[1,2,3]\n}"},
		{input: "{\n\"values\":[1,2,3\n", want: "{\n\"values\":[1,2,3]}\n"},
		{input: "NumberLong(\"2\")", want: "\"2\""},
		{input: "{\"_id\":ObjectId(\"123\")}", want: "{\"_id\":\"123\"}"},
		{input: "{\n   \"_id\" : ObjectId(\"123\"),\n   \"isoDate\" : ISODate(\"2012-12-19T06:01:17.171Z\"),\n   \"regularNumber\" : 67,\n   \"long\" : NumberLong(\"2\"),\n   \"long2\" : NumberLong(2),\n   \"int\" : NumberInt(\"3\"),\n   \"int2\" : NumberInt(3),\n   \"decimal\" : NumberDecimal(\"4\"),\n   \"decimal2\" : NumberDecimal(4)\n}", want: "{\n   \"_id\" : \"123\",\n   \"isoDate\" : \"2012-12-19T06:01:17.171Z\",\n   \"regularNumber\" : 67,\n   \"long\" : \"2\",\n   \"long2\" : 2,\n   \"int\" : \"3\",\n   \"int2\" : 3,\n   \"decimal\" : \"4\",\n   \"decimal2\" : 4\n}"},
		{input: "hello world", want: "\"hello world\""},
		{input: "She said: no way", want: "\"She said: no way\""},
		{input: "[\"This is C(2)\", \"This is F(3)]", want: "[\"This is C(2)\", \"This is F(3)\"]"},
		{input: "[\"This is C(2)\", This is F(3)]", want: "[\"This is C(2)\", \"This is F(3)\"]"},
		{input: "True", want: "true"},
		{input: "False", want: "false"},
		{input: "None", want: "null"},
		{input: "foo", want: "\"foo\""},
		{input: "[1,foo,4]", want: "[1,\"foo\",4]"},
		{input: "{foo: bar}", want: "{\"foo\": \"bar\"}"},
		{input: "foo 2 bar", want: "\"foo 2 bar\""},
		{input: "{greeting: hello world}", want: "{\"greeting\": \"hello world\"}"},
		{input: "{greeting: hello world\nnext: \"line\"}", want: "{\"greeting\": \"hello world\",\n\"next\": \"line\"}"},
		{input: "{greeting: hello world!}", want: "{\"greeting\": \"hello world!\"}"},
		{input: "ES2020", want: "\"ES2020\""},
		{input: "0.0.1", want: "\"0.0.1\""},
		{input: "746de9ad-d4ff-4c66-97d7-00a92ad46967", want: "\"746de9ad-d4ff-4c66-97d7-00a92ad46967\""},
		{input: "234..5", want: "\"234..5\""},
		{input: "[0.0.1,2]", want: "[\"0.0.1\",2]"},
		{input: "[2 0.0.1 2]", want: "[2, \"0.0.1 2\"]"},
		{input: "2e3.4", want: "\"2e3.4\""},
		{input: "{regex: /standalone-styles.css/}", want: "{\"regex\": \"/standalone-styles.css/\"}"},
		{input: "/[a-z]_/", want: "\"/[a-z]_/\""},
		{input: "/\\//", want: "\"/\\\\//\""},
		{input: "/foo\"; console.log(-1); \"/", want: "\"/foo\\\"; console.log(-1); \\\"/\""},
		{input: "\"hello\" + \" world\"", want: "\"hello world\""},
		{input: "\"hello\" +\n \" world\"", want: "\"hello world\""},
		{input: "\"a\"+\"b\"+\"c\"", want: "\"abc\""},
		{input: "\"hello\" + /*comment*/ \" world\"", want: "\"hello world\""},
		{input: "{\n  \"greeting\": 'hello' +\n 'world'\n}", want: "{\n  \"greeting\": \"helloworld\"\n}"},
		{input: "\"hello +\n \" world\"", want: "\"hello world\""},
		{input: "\"hello +", want: "\"hello\""},
		{input: "[\"hello +]", want: "[\"hello\"]"},
		{input: "{\"array\": [{}{}]}", want: "{\"array\": [{},{}]}"},
		{input: "{\"array\": [{} {}]}", want: "{\"array\": [{}, {}]}"},
		{input: "{\"array\": [{}\n{}]}", want: "{\"array\": [{},\n{}]}"},
		{input: "{\"array\": [\n{}\n{}\n]}", want: "{\"array\": [\n{},\n{}\n]}"},
		{input: "{\"array\": [\n1\n2\n]}", want: "{\"array\": [\n1,\n2\n]}"},
		{input: "{\"array\": [\n\"a\"\n\"b\"\n]}", want: "{\"array\": [\n\"a\",\n\"b\"\n]}"},
		{input: "[\n{},\n{}\n]", want: "[\n{},\n{}\n]"},
		{input: "{\"a\":2\n\"b\":3\n}", want: "{\"a\":2,\n\"b\":3\n}"},
		{input: "{\"a\":2\n\"b\":3\nc:4}", want: "{\"a\":2,\n\"b\":3,\n\"c\":4}"},
		{input: "{\n  \"firstName\": \"John\"\n  lastName: Smith", want: "{\n  \"firstName\": \"John\",\n  \"lastName\": \"Smith\"}"},
		{input: "{\n  \"firstName\": \"John\" /* comment */ \n  lastName: Smith", want: "{\n  \"firstName\": \"John\",  \n  \"lastName\": \"Smith\"}"},
		{input: "{\n  \"firstName\": \"John\"\n  ,  lastName: Smith", want: "{\n  \"firstName\": \"John\"\n  ,  \"lastName\": \"Smith\"}"},
		{input: "{\"a\":2.", want: "{\"a\":2.0}"},
		{input: "{\"a\":2e", want: "{\"a\":2e0}"},
		{input: "{\"a\":2e-", want: "{\"a\":2e-0}"},
		{input: "{\"a\":-", want: "{\"a\":-0}"},
		{input: "[2e,", want: "[2e0]"},
		{input: "[2e ", want: "[2e0] "},
		{input: "[-,", want: "[-0]"},
		{input: "{\"a\" \"b\"}", want: "{\"a\": \"b\"}"},
		{input: "{\"a\" 2}", want: "{\"a\": 2}"},
		{input: "{\"a\" true}", want: "{\"a\": true}"},
		{input: "{\"a\" false}", want: "{\"a\": false}"},
		{input: "{\"a\" null}", want: "{\"a\": null}"},
		{input: "{\"a\"2}", want: "{\"a\":2}"},
		{input: "{\n\"a\" \"b\"\n}", want: "{\n\"a\": \"b\"\n}"},
		{input: "{\"a\" 'b'}", want: "{\"a\": \"b\"}"},
		{input: "{'a' 'b'}", want: "{\"a\": \"b\"}"},
		{input: "{\u201ca\u201d \u201cb\u201d}", want: "{\"a\": \"b\"}"},
		{input: "{a 'b'}", want: "{\"a\": \"b\"}"},
		{input: "{a \u201cb\u201d}", want: "{\"a\": \"b\"}"},
		{input: "{\"array\": [\na\nb\n]}", want: "{\"array\": [\n\"a\",\n\"b\"\n]}"},
		{input: "1\n2", want: "[\n1,\n2\n]"},
		{input: "[a,b\nc]", want: "[\"a\",\"b\",\n\"c\"]"},
		{input: "/* 1 */\n{}\n\n/* 2 */\n{}\n\n/* 3 */\n{}\n", want: "[\n\n{},\n\n\n{},\n\n\n{}\n\n]"},
		{input: "/* 1 */\n{},\n\n/* 2 */\n{},\n\n/* 3 */\n{}\n", want: "[\n\n{},\n\n\n{},\n\n\n{}\n\n]"},
		{input: "/* 1 */\n{},\n\n/* 2 */\n{},\n\n/* 3 */\n{},\n", want: "[\n\n{},\n\n\n{},\n\n\n{}\n\n]"},
		{input: "1,2,3", want: "[\n1,2,3\n]"},
		{input: "1,2,3,", want: "[\n1,2,3\n]"},
		{input: "1\n2\n3", want: "[\n1,\n2,\n3\n]"},
		{input: "a\nb", want: "[\n\"a\",\n\"b\"\n]"},
		{input: "a,b", want: "[\n\"a\",\"b\"\n]"},
		{input: "0789", want: "\"0789\""},
		{input: "000789", want: "\"000789\""},
		{input: "001.2", want: "\"001.2\""},
		{input: "002e3", want: "\"002e3\""},
		{input: "[0789]", want: "[\"0789\"]"},
		{input: "{value:0789}", want: "{\"value\":\"0789\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Repair([]byte(tt.input))
			require.NoError(t, err, "input=%q", tt.input)
			require.Equal(t, tt.want, string(got), "input=%q", tt.input)
		})
	}
}

// TestRegularRepairer_Repair_ErrorsMatchCases verifies returned errors match expected messages and positions.
func TestRegularRepairer_Repair_ErrorsMatchCases(t *testing.T) {
	tests := []struct {
		input    string
		message  string
		position int
	}{
		{input: "", message: "Unexpected end of json string", position: 0},
		{input: "{\"a\",", message: "Colon expected", position: 4},
		{input: "{:2}", message: "Object key expected", position: 1},
		{input: "{\"a\":2}{}", message: "Unexpected character \"{\"", position: 7},
		{input: "{\"a\" ]", message: "Colon expected", position: 5},
		{input: "{\"a\":2}foo", message: "Unexpected character \"f\"", position: 7},
		{input: "foo [", message: "Unexpected character \"[\"", position: 4},
		{input: "\"\\u26\"", message: "Invalid unicode character \"\\u26\"\"", position: 1},
		{input: "\"\\uZ000\"", message: "Invalid unicode character \"\\uZ000\"", position: 1},
		{input: "\"\\uZ000", message: "Invalid unicode character \"\\uZ000\"", position: 1},
		{input: "\"abc\u0000\"", message: "Invalid character \"\\u0000\"", position: 4},
		{input: "\"abc\u001f\"", message: "Invalid character \"\\u001f\"", position: 4},
		{input: "callback {}", message: "Unexpected character \"{\"", position: 9},
		{input: "/*a*//*b*/ {}", message: "Unexpected character \"{\"", position: 11},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Repair([]byte(tt.input))
			require.Error(t, err, "input=%q", tt.input)
			var repairErr *Error
			require.ErrorAs(t, err, &repairErr)
			require.Equal(t, tt.message, repairErr.Message, "input=%q", tt.input)
			require.Equal(t, tt.position, repairErr.Position, "input=%q", tt.input)
			require.Equal(t, fmt.Sprintf("%s at position %d", tt.message, tt.position), err.Error(), "input=%q", tt.input)
		})
	}
}

// TestRecoverRegularParserError_ReturnsErrorOnRecovered verifies recovered panics are converted to an Error.
func TestRecoverRegularParserError_ReturnsErrorOnRecovered(t *testing.T) {
	parser := &regularParser{text: []rune("abc"), i: 1}

	err := recoverRegularParserError(parser, "boom", nil)
	require.Error(t, err)
	var repairErr *Error
	require.ErrorAs(t, err, &repairErr)
	require.Equal(t, "Unexpected error", repairErr.Message)
	require.Equal(t, 1, repairErr.Position)
}

// TestRegularParser_EarlyExitWhenErrorPresent verifies parser methods return early when an error already exists.
func TestRegularParser_EarlyExitWhenErrorPresent(t *testing.T) {
	parser := &regularParser{text: []rune("{}"), err: &Error{Message: "fail", Position: 0}}

	require.False(t, parser.parseValue())
	require.False(t, parser.parseObject())
	require.False(t, parser.parseArray())
	require.False(t, parser.parseString(false, -1))
	require.False(t, parser.parseKeyword("true", "true"))
	require.False(t, parser.parseConcatenatedString())
}

// TestRepair_ReturnsErrorForInvalidUnicodeInObjectKey verifies invalid unicode escapes in object keys return an error.
func TestRepair_ReturnsErrorForInvalidUnicodeInObjectKey(t *testing.T) {
	_, err := Repair([]byte("{\"\\u12G4\":1}"))
	require.Error(t, err)
	var repairErr *Error
	require.ErrorAs(t, err, &repairErr)
	require.Equal(t, "Invalid unicode character \"\\u12G4\"", repairErr.Message)
	require.Equal(t, 2, repairErr.Position)
}

// TestRepair_ReturnsErrorForInvalidUnicodeInObjectValue verifies invalid unicode escapes in object values return an error.
func TestRepair_ReturnsErrorForInvalidUnicodeInObjectValue(t *testing.T) {
	_, err := Repair([]byte("{\"a\":\"\\u12G4\"}"))
	require.Error(t, err)
	var repairErr *Error
	require.ErrorAs(t, err, &repairErr)
	require.Equal(t, "Invalid unicode character \"\\u12G4\"", repairErr.Message)
	require.Equal(t, 6, repairErr.Position)
}

// TestRepair_ReturnsErrorForInvalidUnicodeInArrayValue verifies parseArray reports errors from invalid values.
func TestRepair_ReturnsErrorForInvalidUnicodeInArrayValue(t *testing.T) {
	_, err := Repair([]byte("[\"\\u12G4\"]"))
	require.Error(t, err)
	var repairErr *Error
	require.ErrorAs(t, err, &repairErr)
	require.Equal(t, "Invalid unicode character \"\\u12G4\"", repairErr.Message)
	require.Equal(t, 2, repairErr.Position)
}

// TestRepair_ClosesStringAfterTruncatedEscape verifies truncated string escapes at EOF are repaired.
func TestRepair_ClosesStringAfterTruncatedEscape(t *testing.T) {
	got, err := Repair([]byte("\"a\\"))
	require.NoError(t, err)
	require.Equal(t, "\"a\"", string(got))
}

// TestRegularParser_ShouldRestartStringAtEOF_ReturnsFalseOnOutOfRange verifies out-of-range indexes do not restart.
func TestRegularParser_ShouldRestartStringAtEOF_ReturnsFalseOnOutOfRange(t *testing.T) {
	parser := &regularParser{text: []rune("a")}

	require.False(t, parser.shouldRestartStringAtEOF(false, -1))
	require.False(t, parser.shouldRestartStringAtEOF(false, 10))
}

// TestRegularParser_ExtendStringWithURLIfNeeded_SkipsNonURLCases verifies URL extension skips invalid states.
func TestRegularParser_ExtendStringWithURLIfNeeded_SkipsNonURLCases(t *testing.T) {
	parser := &regularParser{text: []rune{':'}, i: 1}
	str := []rune{'"'}
	parser.extendStringWithURLIfNeeded(&str, 0)
	require.Equal(t, []rune{'"'}, str)

	parser = &regularParser{text: []rune("a:b"), i: 2}
	str = []rune{'"', 'a'}
	parser.extendStringWithURLIfNeeded(&str, 0)
	require.Equal(t, []rune{'"', 'a'}, str)
}

// TestRegularParser_ExtendUnquotedStringWithURL_SkipsNonURL verifies URL extension skips non-URL prefixes.
func TestRegularParser_ExtendUnquotedStringWithURL_SkipsNonURL(t *testing.T) {
	parser := &regularParser{text: []rune("a:"), i: 2}

	parser.extendUnquotedStringWithURL(0)
	require.Equal(t, 2, parser.i)
}

// TestRegularParser_ParseConcatenatedString_ReturnsProcessedOnError verifies concatenation returns processed when parsing fails.
func TestRegularParser_ParseConcatenatedString_ReturnsProcessedOnError(t *testing.T) {
	parser := &regularParser{text: []rune("+\"\\u12G4\""), output: []rune("\"a\"")}

	processed := parser.parseConcatenatedString()
	require.True(t, processed)
	require.NotNil(t, parser.err)
}

// TestRegularParser_ParseNumberLeadingSign_ReturnsNotOkForNonDigit verifies leading '-' not followed by a digit is rejected.
func TestRegularParser_ParseNumberLeadingSign_ReturnsNotOkForNonDigit(t *testing.T) {
	parser := &regularParser{text: []rune("-x")}

	repaired, ok := parser.parseNumberLeadingSign(0)
	require.False(t, repaired)
	require.False(t, ok)
	require.Equal(t, 0, parser.i)
}

// TestRegularParser_ParseUnquotedFunctionCall_ReturnsTrueWhenValueErrors verifies function wrappers propagate parse errors.
func TestRegularParser_ParseUnquotedFunctionCall_ReturnsTrueWhenValueErrors(t *testing.T) {
	parser := &regularParser{text: []rune("fn(\"\\u12G4\")")}

	require.True(t, parser.parseUnquotedFunctionCall())
	require.NotNil(t, parser.err)
}

// TestJsonRepair_HelperBranches verifies internal helpers cover edge branches.
func TestJsonRepair_HelperBranches(t *testing.T) {
	require.Equal(t, []rune("a"), insertAtIndex([]rune("a"), -1, 'x'))
	require.Equal(t, []rune("a"), insertAtIndex([]rune("a"), 2, 'x'))

	parser := &regularParser{text: []rune("a")}
	require.Equal(t, rune(0), parser.charAt(-1))
	require.Equal(t, rune(0), parser.charAt(10))

	require.Equal(t, "A", escapeControlCharacter('A'))
}

// TestRegularParser_SetError_DoesNotOverwrite verifies setError only records the first error.
func TestRegularParser_SetError_DoesNotOverwrite(t *testing.T) {
	parser := &regularParser{}

	parser.setError("first", 1)
	parser.setError("second", 2)
	require.NotNil(t, parser.err)
	require.Equal(t, "first", parser.err.Message)
	require.Equal(t, 1, parser.err.Position)
}

// TestRegularParser_HandleMissingObjectValue_SetsError verifies the missing value handler sets an error when needed.
func TestRegularParser_HandleMissingObjectValue_SetsError(t *testing.T) {
	parser := &regularParser{}

	parser.handleMissingObjectValue(false, false)
	require.NotNil(t, parser.err)
	require.Equal(t, "Colon expected", parser.err.Message)
	require.Equal(t, 0, parser.err.Position)
}
