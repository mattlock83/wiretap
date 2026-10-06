---
type: "Test"
title: "Create payment"
description: "A scenario that exercises payment creation."
generated:
  by: "example-producer/1.0"
application_knowledge:
  subject_id: "test:create-payment"
sources:
  - resource: "../../evidence/tests/create-payment/primary/attempt.json"
---

# Create payment

The scenario invokes payment creation and asks the test runner to check its
expected outcome. The [selected attempt](../../evidence/tests/create-payment/primary/attempt.json)
records the runner result and retained evidence.

A response payload alone does not establish the full request/response exchange,
consumer field usage, provider agreement or contract coverage.
