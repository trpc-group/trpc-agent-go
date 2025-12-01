# email Example

This example demonstrates how to handle various types of email (qq,gmail,163) using OpenAI-compatible models.

## Features

- **send email**: send email from your_email to other email, you can send whatever you want, but not support attachment

## Quick Start

```bash
# set your open base url,
export OPENAI_BASE_URL="https://api.openai.com/v1"
# Set your API key
export OPENAI_API_KEY="your-api-key-here"

go run main.go -model gpt-4o-mini
```

## Example Session

```
🚀 Send Email  Chat Demo
Model: gpt-5
Streaming: true
Type 'exit' to end the conversation
Available tools: send_email
==================================================
✅ Email chat ready! Session: email-session-1763626040
💡 Try asking questions like:
   -  send an email to zhuangguang5524621@gmail.com
👤 You: send an email to zhuangguang5524621@gmail.com
🤖 Assistant: I can send the email for you. Please provide the following so I can proceed:
- Email account credentials for sending: account name and password
- Subject line
- Email body/content
- Optional: the display “From” name you want to appear
Recipient to confirm: zhuangguang5524621@gmail.com
If you prefer, I can also draft the email content first and share it with you for approval before sending.
👤 You: name:zhuangguang5524621@gmail.com passwd: "xxxxxx" send an email to 1850396756@qq.com   subject: hello content:<html><body><h1>标题</h1><p>内容</p></body></html>
🤖 Assistant: 
🔍 email initiated:
   • email_send_email (ID: call_DxMS5B7zqSCj8jiEVx6pyG56)
     Query: {"auth":{"name":"zhuangguang5524621@gmail.com","password":"xxxxx"},"mail_list":[{"to_email":"1850396756@qq.com","subject":"hello","content":"<html><body><h1>标题</h1><p>内容</p></body></html>"}]}
🔄 send email...
✅ send email results (ID: call_DxMS5B7zqSCj8jiEVx6pyG56): {"message":""}
Your email has been sent successfully.
Details:
- From: zhuangguang5524621@gmail.com
- To: 1850396756@qq.com
- Subject: hello
- Content (HTML):
  <html><body><h1>标题</h1><p>内容</p></body></html>
If you’d like to send more emails or schedule one, let me know the details.
👤 You: exit
👋 Goodbye!
```

## How It Works

1. **Setup**: The example creates an LLM agent with access to the email tool
2. **User Input**: Users can ask to send email
3. **Tool Detection**: The AI automatically decides when to use the email tool or ask more information of send email
4. **Email Send Execution**: The email tool performs send email and returns structured results
5. **Response Generation**: The AI uses the search results to provide informed, up-to-date responses

## API Design & Limitations

### Why These Limitations Exist
1. the send email tool use smtp protocol, mailbox have speed limit of  send email.