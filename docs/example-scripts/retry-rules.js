picotera.hooks.beforeRequest.tap("retry", function (ctx, input) {
  input.next = !(ctx.attempt.currentRetryCount < 2 && ctx.attempt.totalAttemptCount < 5)
  input.delay = ctx.attempt.currentRetryCount * 500
}, 100);
