# Configuración y CLI

Referencia del checkout actual. [Instalación](operations/download-install.md),
[primera aplicación](operations/first-application.md) y
[administración](operations/administration.md) contienen los recorridos completos.
Para campos exactos genera el schema con el binario de tu versión y valida las
reglas entre campos; no uses un ejemplo de otra release como configuración vigente.

```sh
switchboard schema > switchboard-config.schema.json
switchboard validate -config config.json
switchboard doctor -config config.json -db ./state.db
switchboard version -json
switchboard completion bash  # también zsh, fish, powershell
```

`doctor` comprueba configuración/DB, no disponibilidad pública de una app.
`init -config FILE` crea un archivo privado nuevo; `setup -dir DIR` prepara una
instalación local completa. Ambos rechazan sobrescrituras.

## Configuración base

Ejemplo de proxy HTTP local público, sin login:

```json
{
  "listen": "127.0.0.1:8888",
  "admin": {"listen": "127.0.0.1:9090"},
  "routes": [{"host": "app.localhost", "target": "http://127.0.0.1:3000"}]
}
```

```sh
switchboard -config config.json
```

`config.example.json` y `config.quickstart.json` en la raíz contienen ejemplos;
crea tu archivo local y no lo subas a Git. La validación rechaza claves desconocidas.

| Campo | Uso |
|---|---|
| `listen` | Listener HTTP; se ignora cuando TLS usa los listeners públicos fijos |
| `admin.listen` | Administración loopback; predeterminado `127.0.0.1:9090` |
| `admin.socket` | Socket Unix privado; su permiso de archivo concede autoridad local |
| `admin.token` | Referencia de credencial administrativa completa |
| `admin.sites_token` | Referencia de publicador limitado de sitios |
| `runtime_file` | Proyección operativa del servicio: rutas, proyectos y GitHub |
| `routes` | Hosts y destinos, con política opcional |
| `upstream_timeout_seconds` | Espera de headers upstream: 60 s por defecto, máximo 3600; no limita el body |
| `dev_errors` | Diagnósticos locales; mantener `false` en una exposición pública |

El runtime se inicializa explícitamente y no reemplaza listeners, TLS o autoridad
del host. Revisa [propiedad del estado](operations/administration.md#servicio-y-propiedad-del-estado).

## Rutas y acceso

Cada ruta elige **un** destino: `target` (URL), `upstreams` (varias URLs),
`root` (directorio absoluto), `site` (Pages) o `connector` enrolado.
Un host sin puerto atiende en cualquier puerto; uno con puerto coincide solo
allí y tiene prioridad. Hosts desconocidos no revelan el catálogo público.

| Campo de ruta | Comportamiento |
|---|---|
| `health_path` | Probe HTTP; por defecto `/`; no prueba una ruta estática ni un conector |
| `upstreams` | Selección entre orígenes saludables y fallos pasivos |
| `spa` | En rutas estáticas, fallback de paths desconocidos a index |
| `access` ausente | Ruta pública salvo shorthand legado `auth_required` |
| `access: {}` | Requiere una identidad autenticada |
| `access.allowed_emails`, `allowed_domains` | Allowlist por email **verificado**: coincidencias OR |
| `access.required_groups` | Debe tener **todos** los grupos; AND con la allowlist |
| `access.methods` | Métodos configurados admitidos: `passkey`, `oidc` |
| `access.max_auth_age_minutes` | Prueba reciente con passkey, step-up si excede |
| `access.require_device_bound` | Exige passkey no sincronizable |
| `access.context_rule` | Condición con fuentes explícitas; evidencia necesaria desconocida deniega |
| `resource` | Owner/clase/sensibilidad/acciones declaradas; no son evidencia inferida |
| `audit_mode` | `required` o `best_effort` para acceso cuando la familia está habilitada |

Upstream sin conexión responde 502; timeout de headers 504; sin upstream
saludable 503. Un conector desconectado responde 502 y no tiene fallback TCP.
Conectores pueden protegerse mediante `access` en el proceso integrado.

## Login y sesiones

OIDC externo configura `auth.issuer`, `client_id`, `client_secret` y
`redirect_url`. Este último debe coincidir exactamente con el callback autorizado
`/_/auth/callback`. Solo OIDC requiere `cookie_domain` común para privados en
hosts distintos. Login y redirects se restringen a hosts autorizados.

Passkeys nativas necesitan SQLite y:

```json
"auth": {
  "passkeys": {"rp_id": "example.com", "login_url": "https://auth.example.com"},
  "cookie_domain": "example.com"
}
```

Fragmento para una configuración completa, no archivo autónomo. Usa HTTPS salvo
el contexto localhost de desarrollo. Las passkeys quedan ligadas a RP/origen;
cambiar de dominio requiere enrolamiento, no copiar credenciales.

Pueden coexistir OIDC y passkeys: el callback es `login_url` +
`/_/auth/callback`, `provider_name` etiqueta el botón y el visitante elige.
Step-up reciente/dispositivo requiere passkeys; no sustituirlo por SSO silencioso.

Sesiones por defecto: `session_ttl_minutes: 10080` (7 días absolutos) y
`session_idle_minutes: 720` (12 h sin uso). Uso se registra a lo sumo una vez por
minuto. Sin `-db`, sesiones en memoria; con DB migrada, hashes de IDs persistidos.
Reducir TTL limita sesiones existentes sin extender su expiración emitida.
Logout usa POST. Grupos actuales y revocaciones se verifican en el servidor.

El portal de visitante está en `/_/auth/me`, dispositivos en `/_/auth/devices`.
Las privadas fuera de `cookie_domain` con login central passkey usan sesiones
por host; el protocolo y sus límites están en
[host-sessions](architecture/host-sessions.md). No confundir un cambio de código
con aceptación HTTPS/navegadores/dominios reales; ver tracker de `ID-015`.

Invitaciones y recuperación: [primera aplicación](operations/first-application.md).
La identidad administrativa es distinta de la de visitantes y tiene su propio
ciclo de sesiones; los tiempos de esta sección no son los de la consola.

## Secretos

Credenciales admiten `env:NAME` o `file:/ruta/secret`; la fuente debe existir y
no estar vacía. Los prefijos son reservados. La API persiste referencias, no
valores resueltos. Evita secretos inline en instalaciones reales.

Un secreto OIDC no resoluble impide iniciar; el token administrativo no
resoluble rechaza las operaciones que lo requieren. No copies tokens en ejemplos
curl, URLs o chats. Usa el socket del host o referencias del CLI.

## HTTPS y DNS

`tls.enabled: true` usa :443. Sin `dns01`, autocert usa :80 para validación y
redirección; DNS debe llegar a ese servidor. Conserva `tls.cache_dir` persistente.
La política HTTP-01 sigue rutas configuradas: añadir un host autorizado no
emite certificados para cualquier SNI ni exige reiniciar la whitelist de rutas.

DNS-01 usa `tls.domains` y un bloque `tls.dns01`:

```json
"dns01": {
  "state_dir": "/var/lib/switchboard/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_token": "file:/etc/switchboard/cloudflare-dns.token"
}
```

Va dentro de `tls`; su adaptador actual es Cloudflare. El conjunto acotado de
nombres puede incluir wildcard de un nivel y apex adicional. TLS-ALPN/HTTP no
son necesarios para esa emisión. El estado guarda cuenta, generaciones y cleanup;
un segundo escritor se rechaza. Cambiar estas opciones requiere reinicio.

Staging usa otra carpeta y roots explícitos por `certificate_roots_file`,
que reemplazan los roots de validación de esa emisión; no usarlo para debilitar
producción. La renovación conserva el certificado válido y sin él falla TLS.

`tls.origin_client_ca_file` exige certificado cliente de la CA autorizada en el
listener público: es confianza de proxy, separada de TLS servidor y del túnel.
[Condiciones de despliegue](security.md#entrada-directa-o-detras-de-un-proxy).

Para el asistente de conexión, `dns.target` indica IP o hostname público,
`dns.proxied` el modo del registro y el opcional `dns.cloudflare.token` habilita
creación automática. Sin token se muestra el registro manual. Nunca reemplazar
silenciosamente un registro que podría servir un sitio existente.

## IP de visitante

`trusted_proxies` contiene IPs/CIDRs literales de peers confiables. Vacío significa
no confiar en headers. Solo se admite un `CF-Connecting-IP` válido de esos peers;
valores repetidos, ambiguos, no unicast o con scope se descartan. Nunca agregar
un rango de visitantes o catch-all por comodidad.

Trust limpia headers de IP/identidad y sus aliases; el proxy reconstruye un
`X-Forwarded-For` a partir de la IP seleccionada y no reenvía la cookie de
sesión. `RemoteAddr` sigue siendo el peer de transporte. Loopback confiable da
esa autoridad a procesos locales, no autentica a cloudflared como proceso.

## Rate limiting y Telegram

Sin `rate_limit` no hay buckets configurados. Ejemplo:

```json
"rate_limit": {
  "general": {"rate": 20, "burst": 40},
  "auth": {"rate": 1, "burst": 3},
  "identity": {"rate": 10, "burst": 30},
  "max_keys": 10000
}
```

Rate son requests/s; keys incluyen host e IP confiable. Identidad se cobra después
del allow de política. Capacidad de keys acotada; al llenarse se rechazan keys
nuevas. Respuesta 429 con Retry-After; buckets sobreviven reload si la configuración
no cambia. No equivale a protección volumétrica de red.

`notify.telegram` acepta `token` referenciado, `chats_file` privado y opcional
`api_base` HTTPS (HTTP solo loopback). Empareja desde el host con
`switchboard notify pair -config config.json` y envía `/pair CODE` al bot en el
chat elegido. El proceso es el único lector de updates; código de uso único y
expiración. Cola acotada, reintentos limitados, sin bloquear el request path;
alertas pueden perderse. Aprobar accesos por Telegram sigue siendo otra función.

## Pages

`pages.dir` mantiene artefactos y `keep` controla retención. Una ruta `root`
sirve archivos fijos; `site` usa el deployment activo de Pages. Subir tar.gz de
archivos regulares de `dist` desde la consola, CLI o CI. Switchboard no compila.

Límites predeterminados del extractor: 100 MiB/archivo, 500 MiB total, 20.000
entradas y 32 niveles. Rechaza enlaces, traversal, dispositivos y archivos de
secretos/repositorio. La inspección de contenido secreto también corresponde al
build; validar nombres no acredita ausencia de secretos en cualquier archivo.

Se validan `_headers` y `_redirects` al publicar; nunca se sirven esos archivos.
Ejemplo de sintaxis, en archivos distintos:

```text
# _redirects
/old /new 301
/api/* https://backend.example.com/:splat 302
# _headers
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

Se admiten redirects 301/302/303/307/308; no rewrites 200 ni reglas 404/410.
No se permite usar reglas de archivos para saltar políticas. Un rollback activa
contenido retenido y no cambia DB, DNS o backend. CI necesita proyecto y hostname
dedicado de deploy; [configuración y desconexión](operations/administration.md#github-y-publicacion).

## Administración y auditoría

`/_/healthz` responde sin credencial y no describe rutas. Estado (`/_/status`,
`/_/api/status`), actividad y APIs requieren autoridad. La consola es privada;
mutaciones reciben bearer con herramienta o autoridad del socket. El CLI usa
`admin.socket` por defecto; `-admin` puede elegir un transporte explícito.
`-db` en comandos de identidad es emergencia offline, no la operación rutinaria.

SIGHUP recarga el estado válido; cambios de listener/identidad/TLS requieren
reinicio. Un error conserva el snapshot previo. Las ediciones operativas
comparan revisión/digest para no sobrescribir cambios concurrentes.

`audit` tiene `dir`, `include` (`access`, `admin`), `admin_mode` y
`max_segment_bytes`. Para `access`, privadas default required y públicas
best_effort; `audit_mode` de ruta permite elección explícita. Para admin,
required es el default cuando se incluye la familia. El intent durable no es
prueba de commit. [Contrato de auditoría](engineering.md#auditoria).

```sh
switchboard audit verify -config config.json
switchboard audit keygen -h
switchboard audit checkpoint -h
switchboard audit repair -dir /ruta/audit  # plan; -apply después de revisar
```

Detén el writer para verificar/checkpoint/repair. El journal no se repara al
arrancar y no ofrece retención/pruning completos. Copia checkpoints y clave
pública fijada a un lugar independiente; la herramienta no demuestra esa custodia.

## Migraciones y herramientas de desarrollo

Usa el binario de la versión correcta con DB migrada; no se aplican migraciones
silenciosamente al iniciar:

```sh
switchboard migrate -db ./state.db status
switchboard migrate up -db ./state.db -backup
scripts/new-migration.sh nombre
```

La carpeta padre debe existir. Migraciones aplicadas son inmutables, numeración
continua y runner dueño de transacciones: no añadir BEGIN/COMMIT al SQL. No
limitar la lista de tablas a la de una release antigua; el schema y sus archivos
embebidos son la autoridad. [Verificación](engineering.md#verificacion).
