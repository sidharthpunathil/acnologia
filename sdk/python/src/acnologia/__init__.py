"""Client for Acnologia, a self-hosted API key pool with automatic rotation."""

from ._client import AsyncAcnologia, AsyncProxy, Acnologia, Proxy, __version__
from ._errors import AllKeysBusy, AcnologiaError, InvalidKey, KeyProblem, NoKeyAvailable, QuotaExhausted, RateLimited
from ._models import OUTCOMES, AsyncLease, Auth, Lease, Service

__all__ = [
    "Acnologia",
    "AsyncAcnologia",
    "Proxy",
    "AsyncProxy",
    "Lease",
    "AsyncLease",
    "Service",
    "Auth",
    "OUTCOMES",
    "AcnologiaError",
    "NoKeyAvailable",
    "AllKeysBusy",
    "KeyProblem",
    "RateLimited",
    "QuotaExhausted",
    "InvalidKey",
    "__version__",
]
