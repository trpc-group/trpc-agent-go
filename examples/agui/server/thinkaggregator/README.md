
```json
{
    "type": "MESSAGES_SNAPSHOT",
    "timestamp": 1764764066684,
    "messages": [
        {
            "content": "计算123+456",
            "id": "659606d6-f76d-435f-824e-9385b0ec790b",
            "name": "anonymous",
            "role": "user"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_start",
                "value": null
            },
            "id": "CUSTOM_1764764054441",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_content",
                "value": "\nWe are given the task to calculate 123 + 456.\n We have a calculator tool that can perform operations (add, subtract, multiply, divide, power) on"
            },
            "id": "CUSTOM_1764764055077",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_content",
                "value": " two numbers a and b.\n The operation for addition is \"add\".\n We'll set a = 123, b = 456, operation = \"add\".\n"
            },
            "id": "CUSTOM_1764764055965",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_end",
                "value": null
            },
            "id": "CUSTOM_1764764055965",
            "role": "activity"
        },
        {
            "content": "'ll calculate that for you using the calculator tool.\n\n",
            "id": "chatcmpl-0d21c49905b74f1c9a717ff9a8ed884a",
            "name": "demo-app",
            "role": "assistant",
            "toolCalls": [
                {
                    "id": "chatcmpl-tool-b19be9c76ebe44bd928eff476932c6cd",
                    "type": "function",
                    "function": {
                        "name": "calculator",
                        "arguments": "{\"a\": 123, \"b\": 456, \"operation\": \"add\"}"
                    }
                }
            ]
        },
        {
            "content": "{\"result\":579}",
            "id": "40c4fbbe-457a-4184-8e99-2d089f5abea9",
            "role": "tool",
            "toolCallId": "chatcmpl-tool-b19be9c76ebe44bd928eff476932c6cd"
        },
        {
            "content": "\n123 + 456 的计算结果是 **579**。",
            "id": "chatcmpl-e3c3bbb40e36432cb709cdaaa54a2f36",
            "name": "demo-app",
            "role": "assistant"
        }
    ]
}
```