from openai import OpenAI

client = OpenAI(
  base_url = "http://localhost:9898/backend-api/codex"
)

stream = client.responses.create(
    model="gpt-5.6-luna",
    tools=[{"type": "web_search"}],
    input=[
      {
        "type": "message",
        "role": "user",
        "content": [
          {
            "type": "input_text",
            "text": "search latest gpt model"
          }
        ]
      }
    ],
    store=False,
    stream=True,
)

for event in stream:
    print(event)
