# Seguridad y límites de confianza

Switchboard controla el acceso en el gateway; cada aplicación conserva su
autorización interna. Esta guía describe los límites del diseño y las
condiciones de despliegue. No es una certificación ASVS ni una auditoría
completa de una instalación.

## Reglas para desplegar

- La consola y la API administrativa permanecen en loopback/socket privado.
  Accede por un transporte privado; no las publiques como upstream de una ruta.
- Mantén apps privadas en loopback o red controlada: una dirección alternativa
  que evite el gateway puede evitar también sus políticas.
- Usa TLS público válido; no desactives verificación para resolver problemas
  de certificado, pruebas con staging o conexiones al origen.
- Conserva claves privadas en su host. Configura secretos por referencias
  `file:`/`env:` y permisos mínimos. No los pegues en chats, URLs ni repositorios.
- Distingue visitantes, publicadores CI, conectores y administradores. Una
  identidad válida en un ámbito no concede autoridad en los demás.
- Respalda y ensaya recuperación de claves, configuración y datos; una copia
  antigua puede reactivar autorizaciones revocadas. Sigue
  [recuperación](operations/administration.md#backup-y-recuperacion).

## Fronteras del sistema

| Frontera | Control | Condición externa |
|---|---|---|
| Cliente → edge | Hosts configurados, headers de identidad/IP saneados, política y límites | DNS, firewall, capacidad y protección de red |
| Navegador → login | Validación OIDC o WebAuthn, estado/challenges, sesiones y revocación | IdP configurado correctamente, dominio/RP y autenticadores |
| Edge → aplicación | Identidad generada después de autorizar; cookie de sesión retirada | La app solo confía en el gateway autorizado y conserva sus permisos |
| Edge → conector | mTLS, huella enrolada, recursos fijos y revocación | Distribución de CA, claves locales y transporte permitido |
| Operador → administración | Host/socket o sesión con herramienta requerida | Permisos Unix, seguridad del navegador y acceso al host |
| CI → publicación | Handler dedicado, repo/ref/workflow/audiencia/evento y replay | Proteger el workflow, las credenciales y los artefactos de build |
| Proceso → estado | Validación, migraciones, snapshots y permisos privados | Backups, disco, ACL y host confiables |
| Auditoría → evidencia externa | Cadena y checkpoints firmados | Copia y clave pública fijadas fuera del control del host |

El cliente no puede crear identidad confiable con `X-Auth-Request-*`,
`X-Switchboard-Visitor-*` ni variantes con guiones bajos. El túnel transporta
afirmaciones autenticadas por mTLS; no son JWT verificables por cualquier app.
Conservar un login propio de la app es coexistencia, no SSO automático.
Ver [identidad](architecture/identity-login.md) y
[sesiones por host](architecture/host-sessions.md).

<a id="entrada-directa-o-detras-de-un-proxy"></a>

## Entrada directa o detrás de un proxy

Son perfiles de confianza distintos. En entrada directa, la IP procede del
peer TCP. Detrás de Cloudflare, `trusted_proxies` permite `CF-Connecting-IP`
solo desde peers configurados y con un valor no ambiguo. No confiar en rangos
de visitantes ni en una cadena arbitraria de `X-Forwarded-For`.

La autenticación del origen con `tls.origin_client_ca_file` exige el certificado
cliente de la ruta de proxy elegida. Limitar por IP no identifica por sí solo
una zona concreta. Comprueba origen directo por IPv4/IPv6 y nombres alternativos.
Al migrar a entrada directa, prepara un perfil explícito con firewall, TLS e IP
propios; la caída del proxy nunca debe abrir el origen automáticamente.

DNS y HTTP-01 no dependen intrínsecamente de Cloudflare. La automatización DNS-01
actual usa su API. El túnel público genérico y la independencia completa tienen
aceptación separada; un test local no las demuestra.

<a id="auditoria-y-privacidad"></a>

## Auditoría y privacidad

Activity es un feed acotado en memoria y puede perder eventos; no sustituye
el journal durable. Las rutas con auditoría requerida esperan persistencia
antes de reenviar; un fallo debe negar la solicitud, no rebajar el modo.
Un intent administrativo demuestra un intento autorizado, no un commit de
la operación. Ver [contrato técnico](engineering.md#auditoria).

Una cadena local detecta inconsistencias, pero quien controla todos los archivos
puede reescribirla. Los checkpoints solo ayudan si sobreviven fuera de esa
autoridad junto con la clave pública fijada. El verificador no puede demostrar
por sí mismo dónde se conservó la copia. No se promete transparencia frente a
root ni un log público independiente.

Email, IP, sujeto y decisiones pueden ser datos personales. Limita lectores,
retención y copias; no registres cookies, tokens, cuerpos, claves ni errores
crudos de proveedores. TLS y hash-chain no cifran los archivos en reposo.

<a id="que-hay-que-comprobar-en-cada-instalacion"></a>

## Qué hay que comprobar en cada instalación

Prueba una persona permitida y otra denegada, headers falsificados, acceso
directo al origen, revocación y reconexión, reinicio, renovación TLS y restore
en host limpio. Un healthcheck o una prueba de integridad SQLite son parciales.
La compatibilidad con navegadores, el proveedor real y la capacidad bajo carga
necesitan sus propios ensayos.

Un VPS no equivale a una CDN global ni absorbe ataques que saturan su enlace.
El host y root siguen siendo autoridad. No se prometen alta disponibilidad,
aislamiento entre clientes independientes, protección DDoS volumétrica ni
conformidad ASVS por la existencia de tests unitarios.

Para desarrollo y reproducciones usa fixtures, credenciales ficticias y estado
descartable. No pruebes abuso o disponibilidad contra producción. Los pendientes
de seguridad y aceptación permanecen en [TO_IMPLEMENT.md](TO_IMPLEMENT.md).
