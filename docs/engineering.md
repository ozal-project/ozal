# Guía de desarrollo

Contratos vigentes y comandos de verificación en un solo lugar. Lee
[AGENTS.md](../AGENTS.md), [diseño](architecture/design.md) y
[stack](architecture/stack.md) antes de cambiar código. Los estados, prioridades
y criterios pendientes viven únicamente en [TO_IMPLEMENT.md](TO_IMPLEMENT.md);
la evidencia histórica de cierres está en [IMPLEMENTED.md](IMPLEMENTED.md).

<a id="mantenimiento-de-documentacion"></a>

## Mantenimiento de documentación

Las guías de usuario son [instalación](operations/download-install.md),
[aplicaciones](operations/first-application.md),
[administración](operations/administration.md) y [seguridad](security.md).
Actualiza la sección existente junto con el código; no abras un informe o
plan nuevo por cada conversación, prueba o entrega. Estado pendiente al tracker,
cierre con commit a IMPLEMENTED y límites de versión a las notas de release.

`docs/architecture/design.md` y `stack.md` conservan reglas y dependencias.
Los contratos de identidad se mantienen separados mientras se implementa
`ID-015`, para no mezclar una migración de seguridad con esta consolidación.
Los demás contratos cortos están aquí; detalles de formatos pertenecen además
al código y sus regresiones, no a relatos de implementación repetidos.

La consolidación retiró revisiones fechadas, planes extensos y guías de pilotos.
Sus originales se conservan **localmente** en `.local/docs-archive/`, ignorado
por Git. No son dependencias para compilar o entender el producto desde un
checkout limpio. Para nuevos borradores/capturas usa `docs/local/` —también
ignorado— o un directorio temporal. No guardes secretos allí.

Ignorar archivos no oculta versiones ya publicadas en la historia de Git.
Los trackers siguen versionados porque la página pública los importa durante
el build; no duplicar datos ni ocultar archivos necesarios para reproducirlo.

## Publicacion autonoma

`ARCH-010`: MVP elegido por el mantenedor, etapas A+B+C, con C primero:

| Etapa | Resultado | Límite |
|---|---|---|
| C | Login central y sesiones por host para privadas en dominios independientes | Conservar RP de passkeys y migración de sesiones; protocolo y aceptación propios |
| A | Perfil directo, wildcard DNS inicial en cualquier proveedor y HTTP-01 por nombre configurado | Una IP/edge público, autoridad DNS y CA pública siguen siendo necesarios |
| B | Conector saliente público sin VPN previa | No basta quitar la restricción de red privada; presupuesto, enrolamiento y renovación genéricos |

El estado real se registra en el tracker, no en esta tabla de alcance. DNS
autoritativo propio, URLs para terceros, cuentas públicas y flotas quedan fuera
del MVP. E se limita a migrar el piloto propio. `runtime_file` sigue siendo la
autoridad de rutas; posponer una máquina de estados durable hasta que una
operación externa la necesite. No hay despliegue ni cambio DNS implícitos.

Cloudflare es integración opcional como objetivo de portabilidad. Su DNS-01
actual no demuestra portabilidad de esa API; repetir emisión/renovación con
otro proveedor cuando se implemente. La aceptación completa necesita otro
operador, red externa, TLS, acceso permitido/denegado, revocación y recuperación.

## Identidad y aplicaciones

Login nativo, OIDC, expiración, grupos y recuperación se especifican en
[identity-login.md](architecture/identity-login.md). El handoff entre dominios,
audiencias, códigos de un uso, parentesco y logout está en
[host-sessions.md](architecture/host-sessions.md). Su presencia no declara que
la implementación en curso haya sido aceptada.

`Authorize` decide identidad/grupos; `Fresh` decide método, antigüedad y
dispositivo. Un grupo requerido significa AND con los demás. Email allowlists
necesitan email verificado. Contexto desconocido conserva fuente/frescura y
falla cerrado cuando la regla necesita esa evidencia.

`policy explain` usa el mismo motor con identidad/contexto hipotéticos:

```sh
switchboard policy explain -config /ruta/config.json -host app.example.com \
  -email ana@example.com -verified -groups engineering -expect allowed
```

Consulta `policy explain -h` para opciones; no prueba OIDC, TLS, sesión real,
salud ni transporte. `policy replay` evalúa una regla candidata sin mutar la
política; `who-can` y `review` no deben convertir historia ausente en "sin uso".

La app puede conservar su propio login. Headers de gateway solo se aceptan
desde su camino autenticado; la app conserva autorización interna. Afirmaciones
firmadas verificables por la app, intercambio de tokens y Switchboard como
proveedor OIDC tienen alcances distintos y no están concedidos por pasar headers.
`ARCH-009` conserva la decisión pendiente; `ID-004` no se cierra por handoff.

## Tuneles

El proceso integrado usa `tunnel` más rutas `connector`; exige SQLite migrado
y enrolamiento activo. Cambios de ruta recargan snapshots, conservando sesiones;
listener, subred y archivos de identidad requieren reinicio. Offline devuelve
502 sin fallback TCP. La consola distingue conexión del conector de función real
de la aplicación. [Incorporación](operations/administration.md#incorporar-un-conector).

`tunnel edge|connect` son comandos independientes; `tunnel edge` sirve una ruta
anónima. Para políticas de visitante se necesita el proceso integrado. Ambos
requieren `-cert`, `-key`, `-ca` y host; `connect` también `-edge` literal y
`-server-name` verificado. `-tunnel-network` admite solo una subred RFC1918/ULA;
por defecto loopback. Consulta `-h` para las opciones exactas.

El conector configura `-root` o `-upstream`, y puede añadir hasta ocho hosts con
`-route HOST=root:DIR|upstream:URL`; destinos HTTP fijos en loopback sin
credenciales/path/query. El edge nunca elige un origen arbitrario. El lab
`tunnel-spike` usa identidades efímeras y puertos loopback; no es un producto
de ingreso público ni reemplaza credenciales persistentes.

IP y sujeto/email viajan como `X-Switchboard-Visitor-*` solo dentro de mTLS.
`TrustAuthenticatedTunnel` valida valores y los convierte en identidad upstream;
listeners ordinarios eliminan toda afirmación del cliente y la cookie de sesión.
Son afirmaciones autenticadas por el túnel, no firmas verificables por la app.

El código actual comparte 32 solicitudes en vuelo y conserva el cupo de 16
llamado de handshake durante la sesión completa. Separar presupuesto de
handshake/sesión/stream antes de ampliar capacidad; no anunciar escalabilidad
sin carga medida. La revalidación de enrolamiento corre cada cinco segundos;
error del registro niega conexiones nuevas pero conserva sesiones establecidas.

La renovación del piloto es un comando Linux con coordinación SSH y claves
generadas por rol en su host. Hojas de 30 días, umbral de siete días, journal
durable y bloqueo exclusivo. Conserva identidades anteriores y revierte solo
si la huella actual aún pertenece a la transacción; revocación o reemplazo del
operador prevalecen sobre rollback. Una respuesta SSH perdida se reconcilia.
No expone un firmante de CA público ni representa renovación de una flota.
El rollover de CA es una operación separada; no sustituir su raíz al renovar hojas.

## Certificados

HTTP-01/autocert atiende nombres configurados y sigue la tabla de rutas.
DNS-01 usa el gestor propio sobre `x/crypto/acme`; su adaptador actual es
Cloudflare. Wildcard cubre un nivel, no el apex; nombres alternativos deben
validarse y encontrarse en la hoja. No emitir por cualquier SNI recibido.

Mantener cuenta ACME y clave de certificado separadas, generaciones completas
durables antes del swap, lock de un escritor y recuperación de la última
generación válida. Validar cadena, clave/nombres y vigencia contra roots del
sistema o roots explícitos de staging. Nunca mezclar estados staging/producción.

Renovar según la fracción de vida real, no días fijos; una tentativa activa,
timeouts, backoff con jitter y respeto de retry del proveedor. Con fallo se
conserva un certificado todavía válido; sin certificado válido TLS falla cerrado.
El handshake nunca espera emisión. La limpieza TXT elimina solo el registro
propio por identidad; persistir intención y reconciliar creaciones ambiguas.

Origin client-auth y confianza de proxy son independientes de TLS servidor.
La excepción ALPN de autocert no aplica al listener DNS-01. Emitir eventos
seguros sin tokens ni desafíos. Renovación, caída, restart y operación exterior
necesitan aceptación además del test de primera emisión.

## Publicacion

Builds fuera del edge, normalmente GitHub Actions. Un proyecto asocia sitio,
hostnames y publicadores; el endpoint dedicado recibe artefactos, no ejecuta
builds. El admin continúa privado. El publicador puede ser OIDC Actions o un
token limitado por proyecto; GitHub App de descubrimiento es independiente.

OIDC exige issuer/JWKS/RS256, audiencia, vida breve, IDs de repo/propietario,
ref, workflow y eventos autorizados (`push`/`workflow_dispatch`). La ref de
`pull_request_target` no autoriza por sí sola un despliegue. Replay por `jti`
se guarda de forma durable; GET de estado no consume el permiso de upload.
Ver [deployauth](../internal/pages/deployauth) y
[deployapi](../internal/pages/deployapi) para códigos y endpoints exactos.

Autenticar/limitar/registrar intent requerido **antes de consumir el body**.
Archivos regulares, límites de cantidad/tamaño, no enlaces/traversal, rutas
internas/secretos rechazados, manifest inmutable y activación atómica. Site
rollback activa un artefacto retenido, no cambia DNS ni DB. `_headers` y
`_redirects` se validan/aplican pero no se sirven como archivos públicos.

Snapshots de confianza de un upload en curso pueden sobrevivir al disconnect;
no fingir que cancelarlo desde el navegador revierte el servidor. Un intent sin
outcome significa resultado desconocido, no éxito ni rollback probado.
Refs de secretos no se resuelven de vuelta al config. Previews, PRs generados
y CI real tienen tareas/aceptación propias.

## Auditoria

Activity (`events.Broker`) es acotado y puede perder eventos. El journal durable
JSONL tiene secuencia/hash/prev, lock exclusivo, permisos privados y persistencia
según modo. `required` espera append+sync antes de reenviar o mutar; fallo 503.
`best_effort` admite pérdida y la informa por un canal independiente.

Formato v1: cada record incluye `v`, `log_id`, `seq`, `time`, `type`, campos
seguros y `prev`/`hash`. Se hashéan bytes exactos, no JSON canónico universal.
Dominio `switchboard-audit-v1\x00`, secuencia uint64 big endian, prev decodificado,
longitud uint64 big endian y JSON compacto sin el campo hash final. El código
de [audit](../internal/audit) y sus tests fijan la codificación interoperable.
No registrar cookies, secretos, cuerpos, desafíos ni errores crudos.

Checkpoint Ed25519 firma payload v1 con dominio
`switchboard-audit-checkpoint-v1\x00` y orden `v, log_id, seq, hash, time, key_id`.
Se necesita checkpoint y clave pública fijada fuera de la autoridad del host;
la herramienta verifica firma/cadena, no acredita esa independencia por sí sola.
No llamarlo transparencia pública ni protección contra un root comprometido.

Intent administrativo durable registra intento, no commit. Publicación agrega
outcome sin una transacción común entre DB, archivos y JSONL; si el resultado
se guarda pero falla el append, no deshacer contenido fingiendo atomicidad.
La cobertura de login/certificados/túnel y la rotación/cadencia/retención se
comprueban con las tareas activas, no se infieren de una lista de eventos deseados.

Un tail incompleto no se repara al arrancar: detener writer, ejecutar
`audit repair` en modo plan y después `-apply`, comprobar cadena. Corrupción
interior o hash inválido no se acepta como tail truncado reparable. Verificación
y checkpoints offline respetan el lock. [Seguridad](security.md#auditoria-y-privacidad).

## Propuestas pendientes

Shield challenge (`SHD-003`) sigue propuesto: posición antes de identificar,
nonce de uso único, token ligado a host/acción/expiración, clearance firmado,
replay/tamper tests, accesibilidad y fallback explícito; log-only antes de
enforce. No demuestra humanidad ni protección DDoS. Una futura CA privada,
SSO para apps, Merkle/witnesses, AI y multi-edge no son capacidades actuales
por figurar en VISION. Diseño detallado al aprobar la implementación, dentro
de esta guía o su contrato canónico, sin crear otra hoja de ruta.

## Criterio de desarrollo y stack

Evaluación de 2026-10-27: mantener Go, Svelte/Vite, SQLite y stdlib. Go encaja
con red/TLS y distribución binaria; la consola se compila a archivos estáticos,
sin Node en runtime. Reutilizar WebAuthn/OIDC/JOSE/yamux evita implementar sus
primitivas. SQLite local evita otro servicio; multi-edge con estado compartido
o contención medida puede justificar otra base, no suponerlo por anticipación.

Consolidar CI frontend (`REL-014`), presupuestos del túnel, capacidad real,
recuperación y aceptación por otro operador. La CI frontend se estaba corrigiendo
en el trabajo concurrente; seguir el tracker para su resultado. La autorización
pura, snapshots y regresiones de revocación son fortalezas; paquetes de proxy,
config/admin aún concentran responsabilidades. Extraerlas al tocarlas, no
reescribir todo el árbol. Esta evaluación no es una auditoría línea a línea.

## Verificacion

```sh
scripts/verify.sh
go test -race ./internal/auth ./internal/proxy ./internal/tunnel
node scripts/demo/product-acceptance.mjs
node scripts/demo/passkey-acceptance.mjs
node scripts/demo/host-sessions-acceptance.mjs
CHROME=/ruta/chrome node scripts/demo/https-host-sessions-acceptance.mjs
node scripts/demo/site-check.mjs
python3 scripts/smoke-tunnel-configured.py --binary /ruta/binario-de-prueba
```

Los demos necesitan sus dependencias y navegador; usa `SWITCHBOARD_BIN` y
`CHROME` cuando corresponda, consulta el script para prerrequisitos. Ejecutan
fixtures locales; HTTP localhost no acredita HTTPS público ni otros navegadores.
No instales herramientas adicionales implícitamente por un ejemplo.

La aceptación HTTPS de ID-015 requiere Linux, Go, `agent-browser` con soporte
`--ca-cert`, `certutil` (paquete `libnss3-tools`) y el `playwright-core` ya fijado
en `scripts/demo`. Compila un ejecutable de test de `cmd/switchboard`, usa la
configuración autocert de producción con hojas locales precargadas, el routing
real, WebAuthn y SQLite migrado. Todos los listeners usan puertos efímeros en
loopback; la administración del fixture existe solo en `_test.go`. Cierra stdin
para detenerlo y borra estado/certificados temporales al terminar. La CA solo
se importa en el NSS temporal de esa sesión de agent-browser; no se modifica
la confianza del usuario y no se ignoran errores TLS.

Comprueba que Chrome rechaza primero la CA no confiable y, tras confiar en ella,
acepta login y dos apps de sitios distintos: cookies host-only `Secure`,
`HttpOnly`, `SameSite=Lax`, binding del navegador, audiencia del código, código
robado y consumido, política denegada, cookies retiradas antes del upstream,
logout en ambos extremos, usuario deshabilitado, revocación y expiración del
padre. La expiración se provoca solo en filas del fixture; no se acortan reglas
de producción. Incluye viewport móvil con toque y conserva la sesión compartida
de un host bajo `cookie_domain`. Emulación móvil no acredita un teléfono real.
Dos pestañas del mismo host inician handoffs a la vez y canjean sus códigos en
orden inverso: cada una conserva su destino y solo borra su cookie de vínculo.
El driver aborta la navegación interceptada antes de canjear el código, sin
crear documentos artificiales ni bloquear favicons. Comprueba además que un
binding copiado con `Path=/` tampoco llega al upstream. Las regresiones Go
reproducen el conflicto original y prueban que fallar un canje no impide al
otro terminar, junto al filtrado de cookies en rutas directas y por conector.

El test normal `TestDirectHTTPSProfileWithoutCloudflare` valida la plantilla
directa y sirve un certificado desde caché con confianza TLS comprobada. Estos
ensayos **no emiten ni renuevan certificados públicos**, no prueban propagación
DNS ni firewall, no arrancan el binario de distribución completo y no cierran
ID-015/ARCH-010. La aceptación exterior sigue requiriendo CA pública, otra red,
teléfono, renovación, reinicio y recuperación; el conector público de etapa B
conserva su tarea separada.

Para browser QA: 1440/1024/390/320 px, teclado, foco/Escape, cancelación sin
mutación, errores/retry, datos viejos y logout sin datos residuales. Probar
passkeys por dispositivo, pérdida/revocación, callbacks y handoff entre hosts.
En sitio público comprobar sin JS, carga fallida de WASM y Save-Data. Capturas
en directorio temporal/ignorado, no en la documentación del producto.

Para fallos: estado propio, puertos efímeros, secretos ficticios, restart/kill,
origen lento/5xx, expiración, disco lleno/read-only, reloj y recuperación.
Conservar comandos/resultados/versiones mínimos en el cierre de la tarea;
no repetir informes cronológicos. Tests de fixtures no acreditan DNS/proveedor
real, firma de releases ni restore exterior.

Benchmark de ejemplo, con el host quieto y entorno constante:

```sh
GOMAXPROCS=1 go test -run '^$' -bench 'Benchmark(Authorize|Fresh|Proxy)' \
  -benchmem -benchtime=1s -count=5 ./internal/auth ./internal/proxy
go test -run '^$' -bench '^BenchmarkLimiter' -benchmem ./internal/ratelimit
go test -run '^$' -bench '^BenchmarkWriterAppendGrouped' -benchmem ./internal/audit
```

Los benchmarks de proxy usan transporte sintético. La medición histórica
2026-10-25 comparó ~16 µs/op sin auditoría con ~9 ms/op de auditoría requerida
serial en ese host: muestra el costo de persistencia, no throughput ni p99
públicos. Medir listener/TLS, SQLite, auditoría y túnel juntos con almacenamiento
representativo, saturación y carga sostenida antes de declarar capacidad.

Propiedades/fuzz y mutation deben detectar cambios de reglas, no copiar la
implementación. El harness diferencial requiere Caddy/nginx instalado; tests
que omiten esa dependencia no acreditan interoperabilidad. `LAB-*` conserva
los experimentos pendientes y sus límites.
