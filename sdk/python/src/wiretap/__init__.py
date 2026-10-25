"""A small synchronous Wiretap client. Existing HTTP clients keep their behavior."""

from .client import Attempt, Runtime, WiretapError, trace_headers
from .managed import ManagedRuntime

__all__ = ["Attempt", "ManagedRuntime", "Runtime", "WiretapError", "trace_headers"]
