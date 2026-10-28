# Evaluación de desarrollo, implementación y stack

Fecha: 2026-10-27, America/Asuncion.
Referencia de código: `a17be3d`, con trabajo concurrente sin commit en sesiones
por host excluido del veredicto de implementación terminada.
Solicitado por el mantenedor: evaluar el trabajo completo, justificar Go y
Svelte y documentar qué conviene mantener o cambiar.

## Criterio general

Switchboard tiene una base de ingeniería bien trabajada y un stack adecuado
para un gateway autoalojado. Recomiendo mantener Go, Svelte, Vite y SQLite.
La prioridad técnica es consolidar capacidades y demostrar instalación,
capacidad y recuperación por otros operadores.

Hay evidencia de trabajo sobre propiedades difíciles del sistema: decisiones
de acceso explicables, recargas que conservan el estado válido, revocación,
persistencia y cancelación. Eso justifica continuar sobre esta base.
No demuestra todavía que todas las capacidades estén terminadas o aceptadas
en producción, ni que la instalación escale a cualquier carga.

El riesgo principal es la amplitud: identidad, proxy, túneles, certificados,
hosting, DNS, WAF y transparencia tienen ciclos de vida y responsabilidades
de mantenimiento diferentes. Conviene completar el recorrido del MVP antes
de ampliar las superficies que el proyecto administra.

## Alcance y límites de esta evaluación

Se revisaron arquitectura, dependencias, autorización, proxy, recargas,
certificados, túneles, almacenamiento, consola, pruebas y workflows. Es una
evaluación transversal con inspección de caminos representativos, no una
auditoría exhaustiva de cada línea ni una certificación de seguridad.

La revisión original no ejecutó pruebas nuevas, benchmarks, navegador ni
validación de producción. Las cifras de rendimiento citadas pertenecen a
la evidencia histórica del repositorio. La comprobación de esta actualización
documental se registra por separado al final.

El trabajo de sesiones multidominio estaba cambiando durante la revisión.
Sus archivos modificados y el diseño de sesiones por host no se consideran
una capacidad terminada ni un defecto por estar incompletos. El estado activo
de implementación y las decisiones del mantenedor viven en
[TO_IMPLEMENT.md](../TO_IMPLEMENT.md), que prevalece sobre esta fotografía.

## Propiedades bien resueltas

| Propiedad | Evidencia de implementación | Valor |
|---|---|---|
| Autorización explicable | `auth.Authorize` devuelve `Decision` con `Allowed`, `Code` y `Reason`; `Fresh` recibe el tiempo | Las decisiones se prueban sin red y pueden explicarse sin un segundo motor |
| Recarga segura | `Manager.Reload` carga, valida y construye un candidato antes de instalarlo | Un candidato inválido conserva la generación que sirve tráfico |
| Autoridad administrativa explícita | Admin API, socket local, sesiones de operador y herramientas autorizadas | La visibilidad de botones no constituye el permiso del servidor |
| Fallo de auditoría con efecto real | `TestRequiredAccessAuditFailureStopsBeforeUpstream` | El test comprueba que el upstream no recibe una solicitud cuando falla una auditoría requerida |
| Certificados fuera del handshake | `certs.Manager.GetCertificate` consulta la generación activa; `Run` renueva aparte | No bloquear conexiones esperando una emisión; conservar el certificado válido durante un fallo |
| Estado operativo limitado | `operatingConfig` contiene rutas, proyectos y GitHub, con validación y persistencia propias | La operación rutinaria no adquiere autoridad para cambiar listeners o confianza del host |
| Frontend con controles asíncronos | `LatestRequest`, stream cancelable y credencial en memoria | Evitar respuestas viejas y conexiones abandonadas; limitar la persistencia de credenciales |

Fuentes: [autorización](../../internal/auth/authz.go),
[recargas](../../internal/routemanager/manager.go),
[administración](../../internal/admin/server.go),
[regresión de auditoría](../../internal/proxy/proxy_test.go),
[gestor de certificados](../../internal/certs/manager.go),
[configuración operativa](../../internal/config/operating.go),
[solicitudes de consola](../../web/console/src/lib/latestRequest.ts) y
[sesión de consola](../../web/console/src/lib/adminToken.svelte.ts).

La existencia de regresiones relevantes es una señal positiva sobre el método
de desarrollo. Su cantidad no mide cobertura efectiva, y haber leído los tests
no equivale a haberlos ejecutado en esta revisión.

## Go: elección justificada

El trabajo principal de Switchboard es mantener conexiones, atender HTTP,
terminar TLS, gestionar túneles y coordinar estado y procesos de larga vida.
Go encaja con ese trabajo y permite reutilizar `net/http`, `crypto/tls`,
`crypto/x509` y `httputil.ReverseProxy`.

La distribución aprovecha esa elección: GoReleaser configura `CGO_ENABLED=0`,
el driver SQLite es pure Go y la consola se incorpora con `embed.FS`. El
operador no necesita mantener Node ni un proceso de frontend para ejecutar
la consola distribuida. Ver [go.mod](../../go.mod),
[release build](../../.goreleaser.yaml) y
[consola embebida](../../internal/admin/console.go).

La contrapartida es controlar memoria, asignaciones, goroutines, sincronización
y pausas del recolector bajo carga. Las primitivas de concurrencia no eliminan
los límites del diseño ni convierten automáticamente más CPUs en más capacidad.
No se encontró una necesidad que justifique reescribir el núcleo en Rust,
TypeScript u otro lenguaje.

Referencias técnicas: [ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy)
y [FAQ de Go sobre concurrencia](https://go.dev/doc/faq#Concurrency).

## Svelte: elección justificada

La consola contiene formularios, diálogos, permisos por herramienta, sesiones,
eventos en vivo y estados de carga/fallo. Esa interacción justifica componentes
reactivos. Svelte y TypeScript cubren la necesidad sin exigir un servidor de
renderizado adicional.

La propiedad decisiva es la distribución: Vite genera archivos estáticos en
`internal/admin/console` y Go los sirve embebidos. La política CSP restringe
scripts y conexiones al mismo origen. Son decisiones observables en
[vite.config.ts](../../web/console/vite.config.ts),
[build-console.sh](../../scripts/build-console.sh) y
[console.go](../../internal/admin/console.go).

No se identificó una mejora funcional que compense migrar a React o añadir
SvelteKit. React con Vite también puede generar una consola estática; la
preferencia por Svelte se apoya en el encaje actual y en evitar una migración
sin beneficio demostrado, no en afirmar que otras opciones sean incapaces.
Si cambia una necesidad concreta de componentes o integración, se puede
reevaluar con evidencia.

Referencias: [Svelte](https://svelte.dev/docs/svelte/overview) y
[build estático de Vite](https://vite.dev/guide/build.html).

## Criterio sobre el resto del stack

| Pieza | Recomendación | Condición o contrapartida |
|---|---|---|
| `net/http` y reverse proxy estándar | Mantener | No se necesita un framework HTTP para resolver el alcance actual |
| Tailwind y bits-ui | Mantener | Reutilizar componentes; evitar variantes de UI que dupliquen comportamiento |
| SQLite con modernc | Mantener para una instalación individual | Medir escrituras de sesiones, contención y tamaño/costo de distribución |
| SQL explícito y migraciones numeradas | Mantener | Las migraciones y la recuperación deben validarse en upgrades reales |
| Astro para la página pública | Mantener | Tiene un ciclo distinto de la consola; sigue siendo otro build que debe verificarse |
| yamux sobre mTLS | Mantener como base | Separar presupuestos y aceptar el perfil público antes de prometer capacidad |
| Bibliotecas OIDC, JOSE y WebAuthn | Mantener la reutilización | Revisar actualizaciones y compatibilidad; no implementar primitivas/protocolos delicados desde cero |
| Gestor ACME propio | Mantener con alcance acotado y aceptación | Su almacenamiento, limpieza, renovación y reintentos son mantenimiento permanente; reevaluar si el costo supera su utilidad |
| Postgres, Redis, Kubernetes o servicios separados | No incorporarlos por anticipación | Exigir una necesidad medida de estado compartido, escritura, disponibilidad o aislamiento |

SQLite permite un único escritor a la vez, aunque muchas transacciones breves
pueden alternarse adecuadamente. Postgres se justifica al necesitar estado
compartido entre servidores o una concurrencia de escritura que no quepa en el
modelo local. Dos edges independientes no obligan por sí solos a compartir
base de datos. [Criterios oficiales de SQLite](https://www.sqlite.org/whentouse.html).

Esta evaluación no afirma que las versiones fijadas estén libres de
vulnerabilidades: no se ejecutó un nuevo escaneo de dependencias.

## Observaciones y prioridades recomendadas

Las prioridades son de desarrollo, no severidades de vulnerabilidad. Las
observaciones de fuente se distinguen de las pruebas que faltan. Este informe
explica el criterio; el seguimiento se mantiene únicamente en el tracker.

### 1. Verificación obligatoria del frontend en CI

**Observado:** el workflow principal prepara Go y ejecuta `verify.sh`, sin
instalar dependencias de la consola. El gate solo recompila y comprueba assets
embebidos cuando existe `web/console/node_modules`. Un checkout limpio puede
omitir esa comprobación. El release reutiliza el mismo gate sin preparar la
consola. La compilación del sitio también es condicional en el gate general;
su workflow de publicación sí instala dependencias, pero no sustituye un gate
de pull requests.

Fuentes: [ci.yml](../../.github/workflows/ci.yml),
[release.yml](../../.github/workflows/release.yml) y
[verify.sh](../../scripts/verify.sh), sección `[6b]`.

**Recomendación:** job explícito y obligatorio con Node fijado, `npm ci`,
chequeo de tipos, build y comparación del resultado con los assets embebidos.
Probar que un cambio de fuente con assets viejos falla, y que la versión
sincronizada pasa. Los scripts TypeScript requieren una versión de Node
compatible y reproducible. Seguimiento propuesto: `REL-014`.

No se ejecutó una prueba de bypass de CI en este análisis; la observación se
deduce del flujo condicional y de los pasos versionados.

### 2. Separar los límites del túnel antes de ampliar capacidad

**Observado:** `Edge.Serve` crea `slots` con capacidad 16. La goroutine obtiene
un slot antes del handshake y lo devuelve con `defer` al finalizar la sesión,
después de esperar `session.CloseChan` o la cancelación. El presupuesto llamado
`handshake_capacity` también limita conexiones de larga vida del listener.
`NewEdge` tiene además un cupo global de 32 solicitudes.

Fuente: [tunnel.go](../../internal/tunnel/tunnel.go), `NewEdge` y `Edge.Serve`.

**Recomendación:** definir presupuestos distintos para handshakes en curso,
sesiones autenticadas y solicitudes/streams. Mantener límites deliberados,
configuración validada, métricas y rechazo explicable. No quitar límites para
alcanzar un número de conectores. La prueba de capacidad debe demostrar que
sesiones activas no consumen el cupo exclusivo de handshake, conservando el
tope explícito de sesiones y la cancelación. Refinamiento de `TUN-002` y del
perfil público de `ARCH-010`; no se abre un túnel público con este informe.

Es una observación estática de capacidad y semántica del límite, no una
vulnerabilidad confirmada ni una reproducción de carga.

### 3. Medir el camino completo

[performance.md](../performance.md) registra aproximadamente 9 ms/op para la
solicitud sintética con auditoría durable requerida y 16 µs/op sin auditoría,
en el host y escenario documentados. Esperar persistencia explica un costo;
estas cifras históricas no son latencias p95/p99 ni capacidad de producción.

**Recomendación:** medir listener HTTPS, sesión SQLite, política, auditoría,
túnel y upstream en conjunto. Incluir carga sostenida, saturación, almacenamiento
representativo y fallos. Comparar modos de auditoría sin debilitar la semántica
de las rutas que la requieren. Usar `LAB-001` y `AUD-001`.

### 4. Reducir concentración de responsabilidades de forma gradual

Hay decisiones puras, pero la separación de paquetes sigue parcialmente en
transición. Identificación, autorización, routing y forwarding todavía viven
en el proxy, como reconoce [pipeline.go](../../internal/pipeline/pipeline.go).
Configuración y administración también concentran varias responsabilidades.

**Recomendación:** extraer responsabilidades cuando una tarea aprobada toque
el área y el cambio simplifique pruebas o propiedad. Mantener el composition
root y un único orden de seguridad. El número de líneas por sí solo no prueba
un defecto. No iniciar una reorganización global para igualar carpetas al
diagrama. Regla vigente: [design.md](../architecture/design.md).

### 5. Validar instalación y recuperación por terceros

El repositorio contiene más evidencia de implementación que de operación
independiente. El [runbook de recuperación](../operations/backup-restore.md)
explica que una copia antigua puede recuperar autorizaciones revocadas después
del backup; invalidar sesiones no reconcilia por sí solo usuarios, passkeys,
invitaciones y conectores.

**Recomendación:** un operador distinto instala, publica, revoca y restaura
en un host limpio. Medir RPO/RTO, recuperar archivos y claves además de SQLite,
y reconciliar revocaciones antes de devolver servicio. No inferir recuperación
de un simple chequeo de integridad. Seguimiento existente: `REL-012`, `UI-006`,
`TUN-004` y `LAB-004`.

## Criterio de alcance y mantenimiento

El núcleo de producto coherente es publicar y proteger aplicaciones en
infraestructura propia, con un recorrido entendible y decisiones explicables.
Conservar el MVP de `ARCH-010` elegido por el mantenedor y sus gates. DNS
autoritativo propio, URLs para terceros y operación de un servicio compartido
no entran automáticamente al MVP por aparecer en una propuesta.

Cada implementación propia de protocolo o ciclo de vida aumenta obligaciones
de interoperabilidad, seguridad, recuperación y actualizaciones. Revisar ese
costo en certificados, renovación y futuros mecanismos de identidad. Reutilizar
bibliotecas no impide poseer el producto; reducir dependencia de Cloudflare
no exige reescribir todas las piezas de Internet.

Actualizar documentación canónica cuando cambie una capacidad. La revisión
encontró entradas históricas que ya no representan todo el código actual;
conservar historia sin presentarla como estado vigente. No interpretar un
estado histórico de CI o una prueba local como aceptación pública actual.

## Qué cambia por documentar esta evaluación

Se conserva una evaluación con evidencias y prioridades. No se cambia stack,
no se agregan dependencias y no se ejecutan recomendaciones de código. `REL-014`
se registra como recomendación planificada; las demás observaciones se enlazan
con trabajo existente para evitar un segundo backlog. No se cierra una
capacidad ni se afirma que los defectos observados estén corregidos.

## Verificación de esta actualización documental

Comprobado en esta entrega:

- `git diff --check`: pasó.
- Enlaces locales del informe: existen todos los destinos.
- `node --test site/src/lib/tracker.test.ts`: pasó; `REL-014` tiene una única fila.
- `scripts/verify.sh`: se ejecutó y se detuvo en `[1/7] gofmt`, porque
  `internal/passkey/gate.go`, modificado por el trabajo concurrente de sesiones,
  necesita formato. No se modificó ese código ni se ejecutaron las etapas
  posteriores del gate. No se afirma que el gate completo haya pasado.
  Actualización 2026-10-28: el trabajo de sesiones por host (`ID-015`) quedó
  formateado y `scripts/verify.sh` pasó sus siete etapas sobre ese árbol; eso
  no cambia el veredicto ni las observaciones de esta evaluación.

Estas comprobaciones son independientes de la revisión técnica original y no
validan operación pública, carga, seguridad completa ni aceptación de otro
operador. Los cambios de código concurrentes permanecen fuera del alcance
documental.
