"""Exercise the actual packaged patch with HA's narrow auth/storage boundary."""
import asyncio
import compileall
import importlib.util
import sys
import types
from pathlib import Path

# Only the framework boundary is replaced. Credential and group effects are
# exercised through the real packaged view and verified-login hook.
class Denied(Exception):
    pass

class View:
    def json(self, value):
        return value

for name in ("aiohttp", "homeassistant", "homeassistant.components", "homeassistant.components.http", "homeassistant.auth"):
    sys.modules[name] = types.ModuleType(name)
sys.modules["aiohttp"].web = types.SimpleNamespace(HTTPForbidden=Denied, HTTPConflict=Denied)
sys.modules["homeassistant.components.http"].HomeAssistantView = View
sys.modules["homeassistant.auth"].InvalidAuthError = Denied
assert compileall.compile_dir(str(Path(sys.argv[1]).parent), quiet=1)
spec = importlib.util.spec_from_file_location("binding", sys.argv[1])
binding = importlib.util.module_from_spec(spec)
spec.loader.exec_module(binding)


def user(identity, owner=False, admin=False):
    return types.SimpleNamespace(id=identity, is_owner=owner, is_admin=admin or owner, is_active=True, system_generated=False, credentials=[], groups=[types.SimpleNamespace(id="system-admin" if admin or owner else "system-users")])

class Auth:
    def __init__(self, users): self.users = users
    async def async_get_users(self): return self.users
    async def async_get_user_by_credentials(self, credential):
        return next((u for u in self.users if credential in u.credentials), None)
    async def async_link_user(self, account, credential):
        if await self.async_get_user_by_credentials(credential) is not None: raise Denied()
        account.credentials.append(credential)
    async def async_update_user(self, account, group_ids):
        account.groups = [types.SimpleNamespace(id=g) for g in group_ids]
        account.is_admin = "system-admin" in group_ids

class Provider:
    def __init__(self, hass): self.hass = hass
    def async_create_credentials(self, data): return types.SimpleNamespace(auth_provider_type="auth_oidc", auth_provider_id="default", data=data)
    async def async_credentials(self):
        return [c for u in self.hass.auth.users for c in u.credentials if c.auth_provider_type == "auth_oidc"]

class Request(dict):
    def __init__(self, account, data=None): super().__init__(hass_user=account);self.data=data
    async def json(self): return self.data

async def denied(operation):
    try: await operation
    except Denied: return
    raise AssertionError("unauthorized operation succeeded")

async def run():
    owner, household = user("existing-owner", owner=True), user("existing-household")
    local = types.SimpleNamespace(auth_provider_type="homeassistant", auth_provider_id=None, data={"username":"owner","password_hash":"unchanged"})
    owner.credentials.append(local)
    hass = types.SimpleNamespace(auth=Auth([owner,household]), data={})
    provider = Provider(hass)
    policy = {"issuer":"https://id.lab.home","subject":"immutable-owner-subject"}
    hass.data[binding.KEY] = {"policy":policy,"provider":provider,"lock":asyncio.Lock()}
    view = binding.OwnerBindingView(hass)
    payload = dict(policy, userId=owner.id)
    # First household SSO must never race onboarding to become the first owner.
    await denied(binding.verify_login_binding(provider,None,"household",{"role":"system-users"}))
    await denied(view.post(Request(household,dict(payload,userId=household.id))))
    await denied(view.post(Request(owner,dict(payload,subject="household-subject"))))
    assert owner.credentials == [local] and not household.is_owner
    linked = await view.post(Request(owner,payload))
    assert linked["linked"] and linked["userId"] == owner.id
    assert owner.credentials[0] is local and local.data["password_hash"] == "unchanged"
    credentials = list(owner.credentials)
    assert await view.post(Request(owner,payload)) == linked
    assert owner.credentials == credentials
    assert await view.get(Request(owner)) == linked
    # The patch updates an already existing admin's grants after IdP demotion.
    old = provider.async_create_credentials({"sub":"household"})
    household.credentials.append(old);household.is_admin=True;household.groups=[types.SimpleNamespace(id="system-admin")]
    await binding.verify_login_binding(provider,old,"household",{"role":"system-users"})
    assert not household.is_admin and not household.is_owner
    assert household.credentials == [old] and owner.credentials == credentials
    await denied(binding.verify_login_binding(provider,old,"household",{"role":"invalid"}))
    # Conflicting immutable identity may not be stolen from another account.
    owner.credentials.remove(credentials[1]);household.credentials.append(credentials[1])
    await denied(view.post(Request(owner,payload)))
    assert credentials[1] in household.credentials and owner.credentials == [local]

asyncio.run(run())
