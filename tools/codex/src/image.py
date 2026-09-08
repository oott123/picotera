from openai import OpenAI

client = OpenAI(
  base_url = "http://localhost:9898/backend-api/codex"
)

stream = client.responses.create(
    model="gpt-5.6-luna",
    tools=[{"type": "image_generation"}, {"type": "web_search"}],
    input=[
      {
        "type": "message",
        "role": "user",
        "content": [
          {
            "type": "input_text",
            "text": "Search a latest news, then generate an image to explain it."
          }
        ]
      }
    ],
    store=False,
    stream=True,
)

for event in stream:
    print(event)
