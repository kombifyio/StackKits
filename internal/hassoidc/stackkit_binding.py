"""StackKits owner-subject binding for pinned auth_oidc; see README.md."""
import asyncio
import hashlib
import json
from importlib.metadata import version
from aiohttp import web
from homeassistant.components.http import HomeAssistantView
from homeassistant.auth import InvalidAuthError

POLICY_PATH = "/etc/stackkit/home-assistant-identity.json"
KEY = "stackkit_auth_oidc_binding"


def subject_hash(issuer, subject):
    # Exact upstream v1.2.1 identity derivation, not username or email.
    return hashlib.sha256(f"{issuer}.{subject}".encode("utf-8")).hexdigest()


def read_policy():
    with open(POLICY_PATH, encoding="utf-8") as stream:
        policy = json.load(stream)
    if set(policy) != {"issuer", "subject"} or not all(isinstance(v, str) and v for v in policy.values()):
        raise ValueError("Invalid StackKits identity custody")
    return policy


async def register_binding(hass, provider, config):
    # Dependencies supplied by the immutable HA image; all extra dependencies
    # are private vendored packages, with no pip invocation or sys.path changes.
    for name, expected in (("Jinja2", "3.1.6"), ("cryptography", "48.0.1")):
        if version(name) != expected:
            raise ValueError("Home Assistant OIDC dependency differs from its governed pin")
    policy = await hass.async_add_executor_job(read_policy)
    if config.get("discovery_url") != policy["issuer"] + "/.well-known/openid-configuration" or config.get("client_id") != "stackkit-smart-home":
        raise ValueError("Home Assistant OIDC configuration differs from identity custody")
    if config.get("features", {}).get("automatic_user_linking", False):
        raise ValueError("Username-based OIDC account linking is not admitted")
    hass.data[KEY] = {"policy": policy, "provider": provider, "lock": asyncio.Lock()}
    hass.http.register_view(OwnerBindingView(hass))


async def verify_login_binding(provider, credential, sub, meta):
    """Converge existing OIDC role grants; never change owner/local auth."""
    state = provider.hass.data[KEY]
    expected = subject_hash(state["policy"]["issuer"], state["policy"]["subject"])
    # Do not let a first OIDC login race onboarding and become HA's first owner.
    owners = [user for user in await provider.hass.auth.async_get_users() if user.is_owner]
    if len(owners) != 1 or not any(c.auth_provider_type == "auth_oidc" and c.auth_provider_id == "default" and c.data.get("sub") == expected for c in owners[0].credentials):
        raise InvalidAuthError("The local owner must finish OIDC binding first")
    role = meta.get("role")
    if role not in ("system-admin", "system-users"):
        raise InvalidAuthError("OIDC role is not admitted")
    if credential is None:
        if sub == expected:
            raise InvalidAuthError("The owner must complete authenticated account binding first")
        return
    user = await provider.hass.auth.async_get_user_by_credentials(credential)
    if user is None:
        raise InvalidAuthError("Existing OIDC credential has no account")
    if user.is_owner:
        if sub != expected or role != "system-admin":
            raise InvalidAuthError("OIDC credential conflicts with owner authority")
        return
    if sub == expected:
        raise InvalidAuthError("Owner subject is bound to a non-owner account")
    if {group.id for group in user.groups} != {role}:
        await provider.hass.auth.async_update_user(user, group_ids=[role])


class OwnerBindingView(HomeAssistantView):
    url = "/api/stackkit/auth_oidc/owner"
    name = "api:stackkit:auth_oidc:owner"
    requires_auth = True

    def __init__(self, hass):
        self.hass = hass

    def owner(self, request):
        user = request.get("hass_user")
        if user is None or not user.is_owner or not user.is_admin or not user.is_active or user.system_generated:
            raise web.HTTPForbidden()
        return user

    async def binding(self, user, create):
        state = self.hass.data[KEY]
        policy, provider = state["policy"], state["provider"]
        expected = subject_hash(policy["issuer"], policy["subject"])
        async with state["lock"]:
            found = None
            for credential in await provider.async_credentials():
                linked = await self.hass.auth.async_get_user_by_credentials(credential)
                if credential.data.get("sub") == expected:
                    if found is not None or linked is None or linked.id != user.id:
                        raise web.HTTPConflict()
                    found = credential
                elif linked is not None and linked.id == user.id:
                    raise web.HTTPConflict()
            if found is None and create:
                found = provider.async_create_credentials({"sub": expected})
                await self.hass.auth.async_link_user(user, found)
            return {"userId": user.id, "subjectHash": expected, "linked": found is not None}

    async def get(self, request):
        return self.json(await self.binding(self.owner(request), False))

    async def post(self, request):
        user = self.owner(request)
        data = await request.json()
        policy = self.hass.data[KEY]["policy"]
        if not isinstance(data, dict) or set(data) != {"issuer", "subject", "userId"} or data["issuer"] != policy["issuer"] or data["subject"] != policy["subject"] or data["userId"] != user.id:
            raise web.HTTPForbidden()
        return self.json(await self.binding(user, True))
