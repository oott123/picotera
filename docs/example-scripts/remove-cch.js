picotera.hooks.rewriteRequest.tap('remove-cch', function (ctx, input) {
  if (!input.body) return
  const BILLING_HEADER = 'x-anthropic-billing-header:'
  // anthropic messages
  if (input.body.system?.[0]?.text?.startsWith?.(BILLING_HEADER)) {
    input.body.system.splice(0, 1)
    console.log('cch header detected and removed (amsg)')
  }

  // chat completions
  if (input.body.messages?.[0]?.content) {
    const content = input.body.messages?.[0]?.content
    if (typeof content === 'string') {
      if (content.startsWith(BILLING_HEADER)) {
        input.body.messages.splice(0, 1)
        console.log('cch header detected and removed (chat text)')
      }
    } else if (Array.isArray(content)) {
      if (content[0]?.text?.startsWith(BILLING_HEADER)) {
        if (content.length > 1) {
          input.body.messages[0].content.splice(0, 1)
          console.log('cch header detected and removed (chat content piece)')
        } else {
          input.body.messages.splice(0, 1)
          console.log('cch header detected and removed (chat content whole)')
        }
      }
    }
  }

  return input
})
