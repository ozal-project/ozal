# Stack

Decisiones de dependencias. Antes de añadir una, registra aquí motivo y costo
operativo. Versiones exactas: [go.mod](../../go.mod), lockfiles de
[consola](../../web/console/package.json) y [sitio](../../site/package.json).
Versiones de herramientas: `.node-version` y workflows. No duplicar versiones
ni mantener una tabla de estado de tareas en este archivo.

## Decidido

| Área | Elección | Motivo |
|---|---|---|
| Núcleo | Go; mínimo declarado en go.mod | Red, TLS, concurrencia y distribución binaria |
| HTTP y proxy | stdlib net/http, crypto/tls, httputil.ReverseProxy | Sin framework HTTP adicional |
| Certificados públicos | x/crypto/acme/autocert y gestor DNS-01 propio sobre x/crypto/acme | Reutilizar protocolo; mantener almacenamiento/renovación acotados |
| DNS-01: Cloudflare | API Cloudflare con net/http | Adaptador por defecto; sin módulos nuevos |
| DNS-01: Hetzner | API Hetzner Cloud DNS (RRSets, `add_records`/`remove_records` por nombre y tipo, Actions) con net/http | Segundo proveedor administrado, sin módulos nuevos. Esquema tomado de la spec OpenAPI oficial; **el quoting de los TXT no figura en ella** y está aislado en `hetznerTXTValue` hasta una prueba real |
| DNS-01: RFC 2136 | UPDATE firmado con TSIG (RFC 8945) sobre TCP; `golang.org/x/net/dns/dnsmessage` y `crypto/hmac` | Independencia del proveedor DNS (BIND, PowerDNS, Knot). `x/net` ya estaba en `go.mod` como indirecta y pasa a directa: **cero módulos nuevos**. El TSIG propio se contrasta con el `nsupdate` de BIND en un test que se omite si no está instalado; algoritmos SHA-2 solamente |
| OIDC cliente | coreos/go-oidc y x/oauth2 | Verificación de issuer, tokens y flujo; reutilizado para CI |
| JOSE | go-jose/v4 | Firmas y JWT con biblioteca existente |
| Passkeys | go-webauthn/webauthn | No implementar WebAuthn ni sus primitivas desde cero |
| Persistencia | modernc.org/sqlite, SQL explícito y migraciones embebidas | Estado local sin servicio externo ni cgo |
| Túneles | hashicorp/yamux sobre mTLS | Multiplexación reutilizada; identidad y permisos propios |
| Configuración | JSON estricto, referencias de secretos file:/env: | Validación y persistencia sin resolver secretos a disco |
| Consola | Svelte 5, TypeScript, Vite, Tailwind y bits-ui | SPA estática embebida; Node solo en build |
| Página pública | Astro estático, Tailwind y engine Go/WASM opcional | Contenido accesible sin servidor Node |
| Tipografía | Geist Sans/Mono autoalojadas, OFL | Activos locales y CSP sin proveedores de fuentes |
| CLI | flag estándar; ozalctl separado para operación SSH | Instalación/automatización; trabajo rutinario desde consola |
| Telegram/GitHub App | Adaptadores net/http | Opcionales, referencias locales y permisos mínimos |
| Auditoría | JSONL, SHA-256 y checkpoints Ed25519 | Contrato local con límites de evidencia explícitos |
| Pruebas | testing, httptest, race y fuzz nativos | Regresiones en decisiones, adaptadores y fronteras |
| Release | GoReleaser, SHA-256 y SBOM con syft | Herramientas de build; no dependencias del runtime |

La consola se construye en `internal/admin/console` y se sirve con embed.FS.
Vite genera scripts externos compatibles con la CSP del proyecto. El token
administrativo vive en memoria de la pestaña. SvelteKit/SSR no son necesarios
para esta consola; React con Vite sería técnicamente posible, pero no hay una
necesidad demostrada que justifique migrar.

CI Linux y release preparan dependencias con lockfiles y Node fijado, y usan
`OZAL_REQUIRE_FRONTEND=1` para fallar si se omiten controles frontend
(`REL-014`). La aceptación del runner se registra en el tracker; tener el job
configurado no prueba que se haya ejecutado correctamente.

<a id="public-origin-certificate-contract"></a>

## Contrato de certificados públicos

El composition root conecta `GetCertificate` al listener. Cuenta ACME separada
por directorio/entorno; generaciones completas de cert/clave validadas y
persistidas antes de activar. Un escritor, renovación serial con timeouts/backoff,
limpieza por ID de TXT propio y estado de limpieza recuperable. Conservar un
certificado válido si renovar falla; sin él, fallar TLS cerrado.

Wildcard cubre un nivel; apex exige nombre adicional. El gestor acepta el
conjunto acotado de nombres soportado por config; no convertirlo en plataforma
arbitraria sin nueva decisión. Los detalles están en
[ingeniería](../engineering.md#certificados). CertMagic sigue siendo alternativa
si mantener el gestor propio supera el beneficio; no es una dependencia actual.

## Renovación privada

El piloto usa Go crypto/x509 y Ed25519, coordinación SSH y timer systemd.
Claves por host, CSRs públicos y rollback respetando revocación. No requiere
Python/OpenSSL como runtime ni ofrece todavía renovación de cualquier flota.
La CA y su rollover son distintos de las hojas. [Contrato](../engineering.md#tuneles).

## Decidir cuando exista una necesidad

Postgres/estado multi-edge, QUIC, motor WAF, CA privada/SSH, proveedor de correo,
metrics/tracing y primitivas de transparencia conservan sus tareas en el
tracker/VISION. Ninguno se agrega por aparecer en una propuesta. PowerDNS y
un servicio compartido de URLs están fuera del MVP autónomo elegido.

Se conserva la preferencia por stdlib, SQL explícito y dependencias sin cgo.
No usar una DB alojada obligatoria en el camino de acceso del edge. La revisión
consolidada recomienda mantener Go/Svelte/SQLite y priorizar medición,
recuperación y aceptación externa; no demuestra ausencia de vulnerabilidades.
