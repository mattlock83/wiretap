"""Small example of the language-neutral exploration callback protocol.

This is ordinary test support code, not a Python SDK. A test's control function
performs POST requests to /v1/<path>; callbacks run in this same Python process.
"""


from urllib.error import HTTPError


def explore(control, session_id, *, operations, hooks=None, **options):
    callbacks = hooks or {}
    registrations = []
    for event, callback in callbacks.items():
        registration = {"id": event, "event": event}
        if isinstance(callback, dict):
            registration["operations"] = callback["operations"]
        registrations.append(registration)
    job = control("explore", {"session_id": session_id, "operations": operations,
                              "hooks": registrations, **options})
    identity = {"session_id": session_id, "exploration_id": job["exploration_id"]}
    interrupted = None
    callback_failure = None
    replies = {}
    while True:
        try:
            update = control("explore/next", identity)
        except KeyboardInterrupt as error:
            # Continue dispatching cleanup callbacks after cancellation.
            interrupted = error
            control("explore/cancel", identity)
            continue
        action = update.get("action")
        if action:
            if action["id"] in replies:
                control("explore/respond", replies[action["id"]])
                continue
            callback = callbacks[action["hook_id"]]
            if isinstance(callback, dict):
                callback = callback["handler"]
            reply = {**identity, "action_id": action["id"]}
            try:
                reply["output"] = callback(action["input"]) or {}
            except KeyboardInterrupt as error:
                interrupted = error
                control("explore/cancel", identity)
                continue
            except Exception as error:
                reply["error"] = f"{type(error).__name__}: {error}"
            replies[action["id"]] = reply
            try:
                control("explore/respond", reply)
            except HTTPError as error:
                if error.code != 409:
                    raise
                # A hook can time out while local code is still running. Its
                # late reply must not interrupt dispatch of pending cleanup.
                callback_failure = "A local callback exceeded its server deadline"
                control("explore/cancel", identity)
        if update["state"] in {"complete", "failed", "cancelled"}:
            if interrupted:
                raise interrupted
            if callback_failure:
                raise AssertionError(callback_failure)
            if update["state"] != "complete":
                raise AssertionError(update.get("error") or f"Exploration {update['state']}: {update.get('result')}")
            return update["result"]
