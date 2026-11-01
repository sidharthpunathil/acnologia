"""Client for Cogito, a self-hosted API key pool with automatic rotation."""

from ._client import AsyncCogito, AsyncProxy, Cogito, Proxy, __version__
from ._errors import CogitoError, InvalidKey, KeyProblem, NoKeyAvailable, QuotaExhausted, RateLimited
from ._models import OUTCOMES, AsyncLease, Auth, Lease, Service

__all__ = [
    "Cogito",
    "AsyncCogito",
    "Proxy",
    "AsyncProxy",
    "Lease",
    "AsyncLease",
    "Service",
    "Auth",
    "OUTCOMES",
    "CogitoError",
    "NoKeyAvailable",
    "KeyProblem",
    "RateLimited",
    "QuotaExhausted",
    "InvalidKey",
    "__version__",
]
