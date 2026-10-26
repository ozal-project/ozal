# Experiencia de producto: de la descarga al uso diario

Revisión: **2026-10-26**. Base inspeccionada: `cb2f0da` y cambios locales
preexistentes de presentación. Este documento explica el producto completo,
contrasta el código y propone los recorridos que faltaban. El dueño autorizó
su implementación el 26/10/2026. La tabla siguiente actualiza el estado; el
resto del diagnóstico conserva la fotografía anterior. Los estados, prioridades
y criterios de cierre viven únicamente en [TO_IMPLEMENT](../TO_IMPLEMENT.md).

## Implementación autorizada — 26/10/2026

| Área | Implementado en este checkout | Aceptación que no se presume |
|---|---|---|
| Primer recorrido | `setup` crea una prueba protegida persistente; Applications revisa, configura, retira y publica artefactos de sitios existentes. Acceptance local 2026-10-26 pasó bootstrap de operador, limpieza del fragmento, alta de aplicación, upload y rollback; teclado, cuatro viewports y axe 0 violaciones. Capturas 1440/390 revisadas. | Persona externa, hostname final y migración pública |
| Identidad y grupos | Editor y catálogo nativos, conflicto de membresía, baja de sesiones, grupos vigentes por petición; sujetos OIDC por emisor y migración 012. El browser acceptance local pasó cancelación y guardado de la membresía de la persona de fixture. | Proveedor real y dispositivo físico; email/SCIM no se añadieron |
| Sesiones del visitante | Listado propio, cierre de una o de todas, sin IDs bearer ni datos ajenos | Teléfono real y paridad del gate solo OIDC |
| Consola | Bootstrap del host de un solo uso y sesiones temporales identificadas por operador; escucha privada y autoridad host-only conservadas. Acceptance local pasó bootstrap y eliminación del fragmento de credencial. | No hay SaaS ni autenticación civil del nombre afirmado por el host |
| Sitios/dominios | Ficha operativa, carga de tar.gz, edición de destino/acceso, digest contra ediciones obsoletas y asociación atómica de dominios del proyecto. Revisión visual local en 1440/390 y axe sin violaciones. | DNS manual y certificado servido requieren prueba externa |
| GitHub | App propia opcional, metadatos paginados, selección de repo y confianza comprobada por IDs/ref/workflow; desconexión explícita. Estado desconectado revisado en browser local. | Configurar una App real y ejecutar el primer workflow público |
| Conectores | Preparar CSR/clave local, firma con CA existente, verificación, ficha de confianza y caducidad/alcance. Estado vacío revisado en browser local. | Segundo host real y rotación/renovación en ese host |
| Distribución | Instalador Linux verificado sin sobrescritura, guías por sistema y archivos de operación incluidos en releases | Publicación de una versión y acceso de una persona sin permiso al repo |

Guías actuales: [primera aplicación](../operations/first-application.md),
[GitHub](../operations/github-repositories.md),
[conectores](../operations/connector-onboarding.md) y
[descarga/instalación](../operations/download-install.md). Las capacidades de
main no se atribuyen retroactivamente a alpha.1. Ninguna publicación, cambio
DNS o cambio de visibilidad se realizó como parte de esta implementación.

## Respuesta directa

**No está todo cubierto como experiencia de usuario.** El núcleo tiene piezas
útiles de acceso, identidad, túneles y publicación. La persona nueva todavía
tiene que unir configuración, comandos, DNS, certificados, credenciales y
pantallas. Tener esas piezas no equivale a poder instalar y operar el producto
sin ayuda del autor.

La promesa recomendable es: **«Publicá tus aplicaciones y sitios en tu
infraestructura, decidí quién entra y entendé cada decisión»**. El primer
resultado debe ser una aplicación accesible por la persona correcta, una
negativa explicada para quien no tiene permiso y una forma de deshacer el
cambio. La CA, Shield avanzado y agentes de IA no deben ser requisitos para
entender ese recorrido.

## Qué existe y qué falta

«Existe» aquí significa código inspeccionado, no aceptación en producción.
La release `v0.1.0-alpha.1` y el checkout actual son versiones distintas.

| Pregunta | Código actual | Hueco de producto y tarea dueña |
|---|---|---|
| ¿Cómo lo pruebo? | `init`, `validate`, `doctor`, `version`, schema y completado; quickstart con proxy local | `init` genera una ruta pública en loopback, sin identidad ni instalación del servicio. Falta un recorrido protegido completo: `UI-006` |
| ¿Cómo lo descargo? | GoReleaser define seis combinaciones de SO/arquitectura, checksums y SBOM; workflow de release en borrador | Repositorio **privado**, confirmado con el conector GitHub el 26/10; no se verificaron assets remotos en esta revisión. No hay un canal público de descarga comprobado: `REL-012`, `SITE-002` |
| ¿Cómo queda instalado? | Procedimiento systemd y comando `upgrade` con rollback | Instalación manual; sin asistente integral; upgrade no descubre ni autentica un canal de releases: `REL-010`, `REL-012` |
| ¿Cómo entro a administrar? | Consola Svelte embebida; loopback y token administrativo; socket Unix para acciones del host | No hay login administrativo por persona ni roles de consola: `UI-007`. El acceso remoto actual necesita un transporte privado como SSH |
| ¿Cómo agrego personas? | Invitación, passkey, deshabilitar y revocar desde API/consola; recuperación mediante socket del host | Invitación enviada a mano; correo pendiente (`ID-001/002`); listado y cierre individual de sesiones pendientes (`ID-012`) |
| ¿Hay grupos? | Grupos en usuarios nativos y claim `groups` OIDC; `required_groups` en política | No hay catálogo ni edición de membresías; invitar de nuevo no modifica los grupos de una cuenta existente: `ID-013` |
| ¿Puedo ver mis repositorios GitHub? | Proyectos declarados en config; listado administrativo y confianza por repo/ref/workflow | No hay GitHub App, conexión de cuenta ni explorador de repositorios: `PAGES-010` |
| ¿Puedo desplegar desde GitHub? | Endpoint estrecho, identidad OIDC de Actions, tokens por proyecto, historial de despliegues | `PAGES-008` está cerrado; CI real se registra en el documento de deploy. Workflow genérico, aceptación pública y rollback conservan pendientes de `PAGES-004` y `SITE-003` |
| ¿Cómo administro cada sitio? | Historial y rollback en Sites; información de proyectos; rutas y simulador aparte | Falta una ficha que conecte dominio, publicación y acceso; alta de dominios pendiente: `UI-006`, `PAGES-009`, `UI-005` |
| ¿Cómo conecto un servidor? | Registro de conectores, mTLS, reconexión y mapeos explícitos; comandos y API | No hay enrolamiento guiado de equipos ni vista completa de su ciclo de confianza: `TUN-004`, sobre `TUN-002` |
| ¿Qué ve quien usa las apps? | Portal `/_/auth/me`, dispositivos y página de negativa del gate passkey | Sesiones propias, solicitudes de acceso y paridad del gate solo OIDC pendientes: `ID-012`, `CTX-007` |
| ¿Es SSO para cualquier app? | Login del gateway y cabeceras verificadas a través de un transporte confiable | La app puede conservar otro login. Integración de identidad pendiente de `ARCH-009`; proveedor OIDC propio pendiente de `ID-004` |

## Tres personas y tres superficies

1. **Visitante:** abre una aplicación, registra su passkey por invitación,
   vuelve a entrar, ve sus aplicaciones y administra sus dispositivos. Usa el
   host de login; no necesita conocer rutas, JSON, tokens o servidores.
2. **Operador:** instala la instancia, incorpora aplicaciones, invita personas,
   define acceso, diagnostica negativas y vuelve a una publicación anterior.
   Usa la consola privada de su instalación.
3. **Desarrollador/publicador:** conecta un proyecto a CI, produce el artefacto
   y revisa despliegues y sus causas de fallo. No necesita recibir el token
   global del operador para publicar un proyecto.

Operate/Build ya organiza la navegación. **Es una separación de tareas visual,
no una frontera de permisos.** Hoy manda un operador; los publicadores están
acotados por proyecto según la decisión D5 de [deploy-product](deploy-product.md).
Una persona del grupo `ops` de una aplicación no es por eso administradora del
edge. Tampoco publicar un sitio autoriza a leerlo si es privado.

## Consola self-hosted o SaaS

La dirección aceptada en [VISION](../../VISION.md) es self-hosted y excluye
SaaS multiusuario con facturación. La consola ya viene dentro del binario:
**la web de presentación explica y distribuye; cada instalación sirve su propia
consola y conserva identidad, políticas y estado.** No hace falta una cuenta
central de Switchboard ni un servicio nuestro para cada decisión de acceso.

Un operador puede alojar esa instalación en su VPS. Eso sigue siendo
self-hosted aunque acceda desde un navegador remoto; «self-hosted» no exige
que el navegador esté en el mismo equipo. Hoy el acceso es, por ejemplo,
un túnel SSH a `127.0.0.1:9090`; el listener admin conserva loopback y sus
comprobaciones de Host. El portal de visitantes sí vive en el host de login.

Recomendación: terminar este modelo antes de plantear consola hospedada. Un
SaaS introduciría aislamiento entre instalaciones, custodia de credenciales,
disponibilidad, revocación remota y recuperación propios de otro alcance. No
se presupone aprobado. «Self-hosted» tampoco significa interfaz técnica:
las operaciones habituales deben poder resolverse en el navegador.

Para mejorar el acceso remoto a futuro (`UI-007`), definir primero una sesión
administrativa por persona, step-up y autorización propias. No publicar el
admin actual detrás de una ruta genérica ni tratar una cookie de visitante
como token admin. Recuperación de cuenta y enrolamiento de conectores mantienen
su autoridad del host hasta una decisión explícita que la reemplace.

## Descarga, instalación y primer resultado

### Lo que se puede hacer hoy

- Con acceso al checkout, seguir [QUICKSTART](../../QUICKSTART.md) para probar
  el proxy. Prueba transporte local, no onboarding con identidad.
- Para preparar configuración local, `switchboard init -config config.json`
  crea un archivo nuevo y se niega a sobrescribir otro. Luego se edita y se
  valida; no es un asistente de producción.
- Para una instalación persistente, seguir [systemd](../operations/systemd.md),
  configurar referencias a secretos y socket, migrar explícitamente y arrancar
  con `-db`. `doctor` inspecciona config y base; no comprueba DNS público, TLS
  externo o que una aplicación funcione de punta a punta.
- Preparar passkeys o proveedor externo y una ruta protegida según el
  [login nativo](identity-login.md) y README; invitar a la primera persona.
  Guardar una segunda credencial y ensayar la recuperación del host.

No se presenta una URL de descarga pública como disponible: el repositorio es
privado. Para un canal futuro, la página debe mostrar release exacta, sistema,
arquitectura, checksum y qué incluye. No prometer passkeys por descargar una
alpha anterior que no los contiene. El checksum comprueba integridad contra el
valor esperado; no sustituye la procedencia confiable de ese valor o una firma.

### Recorrido propuesto (`UI-006`, `REL-012`)

| Paso | Pregunta para la persona | Resultado observable |
|---|---|---|
| Elegir | «¿Querés probar localmente o instalar un edge?» | Dos recorridos separados; no pedir DNS en la prueba local |
| Preparar | «¿Qué dominio usarás y dónde corre Switchboard?» | Validar permisos, puertos necesarios, carpetas y persistencia antes de cambiar nada |
| Identidad | «¿Invitaciones con passkey o tu proveedor OIDC?» | Explicar dominio de passkeys, host de login y alcance de cookies antes de crear credenciales |
| Primera app | «¿Sitio estático o aplicación que ya corre?» | Destino y política explícitos; sin publicar por defecto una aplicación sensible |
| Primera persona | «¿Quién debería poder abrirla?» | Invitación con estado, caducidad y destino; cuenta de visitante distinta de administración |
| Ensayo | «Probá con una persona permitida y otra sin permiso» | Login, paso al destino, negativa explicada y evento correlacionable |
| Operar | «¿Cómo volvés atrás o recuperás acceso?» | Consola accesible de forma privada, backup/restore ensayado y ayuda para cada fallo |

El bootstrap inicial se autoriza en el host; nunca puede depender de «el primer
visitante público se convierte en dueño». La UI futura debe ofrecer reanudar
pasos sin duplicar cuentas/rutas, detectar una instalación existente y mantener
acceso administrativo mientras se prueba un cambio de identidad. Configurar
una cuenta invitada no transforma esa cuenta en propietaria de la consola.

El cambio de dominio de login/RP ID merece un aviso previo: las passkeys están
ligadas a ese dominio y no se trasladan simplemente copiando archivos. Hace
falta un procedimiento de nueva inscripción o recuperación.

## Si ya tengo una página funcionando

**Es posible integrarla hoy, pero todavía no es un flujo amigable completo.**
Hay dos situaciones y no conviene hacer pasar una por la otra.

### Sitio estático existente

Un Astro, Vite o HTML ya construido puede seguir en su hosting actual mientras
se prepara una copia en Pages. Switchboard recibe el resultado `dist`, no el
repositorio para ejecutar su build. Crear sitio/ruta/proyecto, subir el
artefacto, probar assets, rutas profundas, redirecciones y cabeceras. Conectar
CI es un paso posterior; no es obligatorio tener GitHub para una subida manual.

El rollback de Pages activa una publicación conservada. Si la migración también
cambió DNS o movió un backend, ese rollback no revierte esos otros cambios.

### Aplicación con backend existente

El runtime y su base quedan en su servidor. Switchboard puede estar delante
por red privada/directa, o llegar por un conector que inicia la conexión hacia
el edge cuando el origen está detrás de NAT. Configurar host, destino,
transporte, TLS y política. Un repositorio con servidor Node, Go o Python no se
convierte en sitio estático por conectarlo a GitHub; el binario no ejecuta esos
servicios ni los despliega a sus hosts.

Si la aplicación ya tiene login, elegir explícitamente transporte solamente,
coexistencia o integración de identidad según [ARCH-009](identity-integration.md).
Probar cookies, callback OIDC, URL base, WebSocket, cargas y rutas relevantes.
Una URL temporal puede necesitar callback y configuración propios de la app.
No desactivar su login para evitar una segunda pantalla sin un contrato de
identidad y una frontera de red comprobados.

### Asistente recomendado: «Agregar una web existente»

1. Preguntar qué existe, dónde corre y si debe ser pública o privada.
2. Elegir «subir sitio estático» o «conectar aplicación»; GitHub es opcional.
3. Preparar un hostname temporal con TLS válido; para una app, configurar
   acceso al origen o el conector. Si requiere identidad, crear su política.
4. Comprobar contenido y un caso real; una app privada debe rechazar una
   persona sin permiso. Verificar además que no se pueda eludir el edge por
   el origen anterior. Durante la prueba, mostrar claramente si el antiguo
   endpoint todavía permite el bypass.
5. Mostrar un resumen: dominio final, destino, quién entra y qué cambiará.
   Ofrecer instrucciones DNS manuales; automatización es optativa (`PAGES-009`).
6. Cambiar el dominio solamente tras la aceptación del operador; conservar
   instrucciones y configuración anterior para revertir, considerando cachés
   DNS. Mantener el hosting anterior durante la ventana acordada.
7. Comprobar el dominio final desde fuera y registrar el resultado; una
   comprobación solo desde la VPS no demuestra el acceso del visitante.

Hoy estos pasos son trabajo manual repartido entre CLI, config y consola.
El asistente es propuesta de `UI-006`, con dominios en `PAGES-009` y conectores
en `TUN-004`; no existe todavía un botón que haga todo esto.

## Servidores, conectores y disponibilidad

El modelo que hay que explicar al usuario:

| Elemento | Responsabilidad | Si se apaga |
|---|---|---|
| Edge, por ejemplo en una VPS | TLS, identidad, política, rutas y archivos Pages | Se pierde el acceso servido por ese edge |
| Servidor de aplicación | Ejecutar la app y su base | Esa app deja de responder; Pages no depende de su runtime |
| Conector en red privada | Llevar destinos explícitos al edge por mTLS saliente | Las apps que dependen de él quedan sin transporte; no deben usar una ruta alternativa insegura |
| GitHub Actions o CI | Construir y enviar artefactos | La publicación ya activa sigue disponible; no se generan nuevas desde ese CI |
| Server Ops / herramientas del operador | Instalar procesos, backups, recursos y reinicios | Su ausencia no debe cambiar las decisiones de acceso del gateway |

Switchboard gestiona **la confianza y publicación** de esos destinos: conector
autorizado, certificado y caducidad, conexión, hosts servidos y revocación.
No compra VPS, instala bases, administra systemd de terceros o reemplaza un panel
de procesos. Usar Switchboard no debe exigir instalar Server Ops.

La ficha propuesta de un conector (`TUN-004`) muestra nombre, instancia,
estado y antigüedad del dato, identidad/caducidad y recursos afectados por una
revocación. La instalación debe generar su clave en el equipo destino,
intercambiar únicamente lo necesario por un canal autorizado y mostrar el
alcance antes del enrolamiento. Hoy certificados, registro y renovación tienen
procedimientos técnicos; no hay un «copiar un comando y conectar cualquier
servidor» como producto terminado. Un token de enrolamiento temporal es una
posibilidad de diseño futura, no una capacidad actual.

Un edge con SQLite es el modelo actual; varias instalaciones pueden operarse
por separado. No hay consola de flota ni estado compartido/HA verificados.

## GitHub: tres integraciones distintas

1. **Identidad del workflow que publica:** ya existe. El endpoint verifica
   repo y propietario por ID numérico, ref, workflow, audiencia y token corto;
   protege contra replay. Esto no concede acceso de una persona a la consola.
2. **Conectar GitHub para elegir repositorios:** falta. Recomendación para
   `PAGES-010`: GitHub App con selección explícita de repos y permisos mínimos,
   como adaptador opcional. La instalación debe pertenecer al operador; la
   configuración manual sigue funcionando sin una App central nuestra.
3. **Entrar como persona con GitHub o importar sus equipos:** no está
   implementado. El OIDC de Actions identifica workflows, no visitantes.
   Importar equipos exige contrato de origen, sincronización, revocación y
   caducidad; no se obtiene automáticamente al conectar un repo.

Para la App, empezar con lectura de metadatos para elegir repositorios y
confirmar los permisos precisos de cada endpoint al implementarlo. Escribir
workflows o abrir PRs son permisos separados: no pedir escritura global para
un explorador. La selección de repos e instalación acotada son capacidades
oficiales de [GitHub Apps](https://docs.github.com/en/apps/using-github-apps/installing-a-github-app-from-a-third-party);
la asignación de permisos se documenta por [operación](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app).
La distinción con la identidad de CI se apoya en la [referencia OIDC de Actions](https://docs.github.com/en/actions/reference/security/oidc).

Recorrido propuesto: conectar instalación, listar solo repos autorizados,
elegir repo y rama, asociar un proyecto, obtener el workflow para revisión,
configurar variables/secrets en GitHub, ejecutar y comprobar el primer deploy.
Antes de habilitar publicación, confirmar repo/owner por IDs. Quitar la App
revoca su acceso API; no revoca por sí mismo una regla OIDC de deploy ya
configurada. La desconexión debe explicar y ejecutar el cambio de confianza
correspondiente, sin borrar el sitio vivo por sorpresa.

El build sigue en CI. Las variables públicas terminan en el navegador, los
secretos de build quedan en GitHub y los de runtime en el host de la app,
según D1/D3. Un proyecto publica; un grupo de visitantes accede. Son permisos
distintos.

## Identidad y grupos

### Semántica actual que la UI debe respetar

- Sin política, una ruta puede ser pública. `access: {}` exige sesión válida,
  pero permite cualquier identidad autenticada aceptada por ese gate.
- `allowed_emails` y `allowed_domains` son alternativas; si se configuran,
  requieren correo marcado verificado. Además se exigen **todos** los
  `required_groups`. Los nombres de grupos distinguen mayúsculas/minúsculas.
- Por tanto `["ops", "engineering"]` exige pertenecer a ambos. No significa
  «ops o engineering». Un editor visual no puede cambiar esa semántica sin
  modificar y probar el núcleo y su configuración.
- En login nativo el operador afirma correo y grupos al crear la invitación;
  no se demuestra posesión del buzón mediante email. Una passkey demuestra
  control de la credencial registrada, no identidad civil ni estado del equipo.
- En OIDC, correo verificado y grupos proceden de los claims del proveedor.
  Si falta el claim de grupos, no se satisface una política que los requiere.
- Deshabilitar o revocar una passkey nativa termina sus sesiones. Eso no
  representa una revocación del usuario en su proveedor OIDC externo.

Ejemplo actual: para que solo el grupo `ops` entre a Grafana,
`"access": {"required_groups": ["ops"]}`. Invitar a Ana con `-group ops`
no le da derechos para editar rutas ni publicar una web. Para un equipo pequeño,
grupos explícitos más excepciones por correo son un punto inicial razonable;
roles administrativos por proyecto pertenecen a una ampliación de D5.

### Gestión de grupos propuesta (`ID-013`)

Añadir catálogo, miembros y aplicaciones afectadas; permitir alta/baja de
membresía con revisión de consecuencias y evento. Al quitar una membresía,
invalidar o actualizar sus sesiones de forma definida y probar que el siguiente
acceso pierde el permiso. No editar SQLite manualmente ni usar recuperación
como sustituto de editar grupos. Hoy los grupos se copian a la sesión y el gate
vuelve a comprobar que el usuario esté habilitado, sin refrescar sus grupos.

Mostrar autoridad: «local» o «proveedor OIDC». No editar silenciosamente un
grupo de un IdP desde una pantalla local. Sin sincronización, los claims de
OIDC conservan el valor capturado al login; explicarlo junto a su vigencia.
Si se incorporan grupos GitHub/SCIM, definir primero la fuente y qué pasa cuando
la fuente está caída. No unir grupos de distintas fuentes por un nombre igual.

### Mejorar el concepto de identidad (`ID-014`)

Separar persona estable, credenciales, métodos/proveedores, sesiones y
permisos. Una persona puede tener dos passkeys; una passkey no es otra persona.
Tampoco se debe fusionar automáticamente una cuenta nativa y una OIDC porque
comparten correo. La vinculación necesita prueba de ambas identidades o una
acción explícita con la autoridad y recuperación acordadas.

Hay un desfase concreto: [identity-login](identity-login.md) propone
`oidc:<issuer>|<sub>`, pero `auth/oidc.go` asigna hoy el `sub` crudo del ID token;
las cuentas nativas usan `sb:<id>`. Antes de ampliar proveedores o vincular
cuentas, definir namespaces y migración de sesiones, probar colisiones y evitar
que un `sub` externo se interprete como cuenta nativa. Esta revisión registra
el riesgo de ambigüedad; no demuestra una explotación en una instalación real.

Prioridad de identidad: ciclo completo de alta, acceso, baja y recuperación;
segunda credencial; sesiones propias y negativas útiles. Biometría remota,
scoring y convertirse en IdP no son necesarios para completar esa utilidad.

## Cómo debería gestionarse cada página de la consola

| Pantalla | Decisión útil | Comportamiento a completar |
|---|---|---|
| Inicio | «¿Qué necesita mi atención?» | Alertas accionables con recurso, causa, edad del dato y siguiente paso; no confundir contador de eventos en memoria con historial completo |
| Aplicaciones y sitios | «¿Qué estoy publicando?» | Lista por recurso con nombre, URL, tipo, acceso y publicación; mostrar si está pendiente de DNS/TLS/primer deploy |
| Ficha de sitio/app | «¿Cómo está configurada y cómo vuelvo atrás?» | Resumen; dominios; acceso; despliegues si es estático; conector si es app; actividad. Una sola ficha enlaza las vistas existentes |
| Personas y grupos | «¿Quién puede entrar y cómo lo revoco?» | Estado de invitación y autoridad, grupos, dispositivos, apps afectadas; edición de membresías y consecuencias explícitas |
| Accesos | «¿Por qué entró o no entró?» | Referencia de decisión, filtros, vínculo al recurso y al simulador con contexto seguro; retención y cobertura declaradas |
| Conectores | «¿Qué red o equipo da acceso a estas apps?» | Confianza, alcance, estado, vencimiento y efecto de revocar; separar de salud del host |
| Configuración técnica | «¿Qué condición está evaluando el edge?» | Rutas, políticas, confianza de CI, simulación y diagnóstico; sin obligar a usar JSON para operaciones rutinarias |
| Portal personal | «¿Qué puedo abrir y dónde sigo conectado?» | Apps autorizadas, dispositivos, sesiones propias y salida ante una negativa; nunca catálogo global para visitantes |

Son propuestas sobre las pantallas actuales, no ocho módulos nuevos obligatorios.
Para un sitio publicado, «gestionar la página» significa gestionar dominio,
acceso y versiones del sitio. Pages no es un CMS ni un editor de contenido:
los textos y componentes se editan en el proyecto que produce el artefacto.
La política de acceso actual pertenece a la ruta por hostname; no hay un
editor de permisos independiente para cada documento `/pagina` del sitio.
Las reglas de contexto pueden considerar clases de paths y acciones, pero
no sustituyen los permisos internos de una aplicación.

Operar y configurar comparten una API y decisiones del servidor: no duplicar el
motor de política en JavaScript. Guardar debe mostrar qué versión se editó y
reconciliar cambios concurrentes; cancelar una operación en el navegador no
prueba que el servidor la haya revertido.

Estados necesarios: vacío con siguiente acción, carga, error con reintento,
permiso insuficiente, función sin configurar y datos desactualizados. En una
acción sensible, indicar objetivo y alcance antes de enviar; después mostrar
resultado confirmado o incertidumbre a reconciliar. Para rollback, indicar
publicación actual y destino; para baja, aplicaciones/sesiones afectadas.
Teclado, móvil, foco, contraste y ausencia de filtraciones forman parte de la
aceptación de `UI-004/005` y `LAB-007`.

## Qué debe decir la presentación pública

Explicar en este orden: problema concreto, demostración actual, dos casos
(publicar sitio y proteger app existente), cómo probar/instalar, límites y
estado. Ofrecer «Soy operador», «Uso una aplicación» y «Publico desde CI» como
entradas a guías reales. Las capturas deben distinguir presente de diseño.

El CTA de descarga depende de disponibilidad real. Repositorio privado y
release inaccesible al visitante requieren un mensaje explícito; una página
pública no vuelve público el código. No confundir una demo WASM del autorizador
con prueba de login, conectores, TLS o instalación. El changelog/roadmap puede
seguir visible aparte, sin obligar a leerlo para comprender el producto.

La presentación tiene trabajo local preexistente `SITE-002/003/004`; esta
revisión no lo reemplaza ni publica esos cambios.

## Orden de trabajo y prueba de utilidad

El orden propuesto se incorpora al tracker; no hay un segundo backlog aquí.
Primero cerrar instalación y primer recorrido con una app existente. Luego
conectar administración de acceso, grupos y ciclo de identidad. Completar CI,
dominios y ficha de recursos; después facilitar selección de repositorios y
enrolamiento. Posponer SaaS y nuevas superficies hasta observar utilidad.

Prueba de aceptación transversal: una persona ajena al autor, con una máquina
limpia y solo la documentación del canal elegido, instala la versión anunciada,
incorpora una web existente en un host de prueba, invita a otra persona,
comprueba acceso/negativa, cambia una membresía, publica y revierte un sitio,
reinicia y recupera el estado. Registrar pasos que necesitaron ayuda, tiempo
real y fallos. No fijar una promesa de «cinco minutos» antes de medirla.

## Evidencia y límites de esta revisión

Se inspeccionaron los contratos de dirección/arquitectura, trackers, CLI,
handlers administrativos, gate de identidad, política pura, almacén de identidad,
portal, modelos de proyectos, navegación/API de consola y configuración de
release. Puntos verificables:

| Archivo | Evidencia |
|---|---|
| [`quick.go`](../../cmd/switchboard/quick.go) | `runInit`, `runDoctor`, alcance local y diagnóstico de base |
| [`server.go`](../../internal/admin/server.go), [`local.go`](../../internal/admin/local.go) | Registro de endpoints y autoridad bearer/socket |
| [`people.go`](../../internal/admin/people.go), [`identity.go`](../../internal/store/sqlite/identity.go) | Invitaciones, baja/revocación y ausencia de actualización de membresías |
| [`authz.go`](../../internal/auth/authz.go) | OR entre allowlists y AND de grupos; step-up separado |
| [`oidc.go`](../../internal/auth/oidc.go), [`gate.go`](../../internal/passkey/gate.go) | `sub` externo crudo, sesiones y comprobación de usuario habilitado |
| [`me.go`](../../internal/passkey/me.go), [`denied.go`](../../internal/passkey/denied.go) | Portal filtrado por política y negativa para visitantes |
| [`projects.go`](../../internal/config/projects.go), [`deployauth`](../../internal/pages/deployauth/decision.go) | Proyecto estático, confianza de deploy, sin conexión de cuenta GitHub |
| [`App.svelte`](../../web/console/src/App.svelte), [`api.ts`](../../web/console/src/lib/api.ts) | Páginas y operaciones que hoy ofrece la consola |
| [`connectors.go`](../../internal/admin/connectors.go) | Listado por bearer; enrolar/rotar/revocar mediante autoridad local |
| [GoReleaser](../../.goreleaser.yaml), [workflow](../../.github/workflows/release.yml) | Plataformas y artefactos configurados; release en borrador |

No es una auditoría exhaustiva de seguridad, una prueba de UX en navegador ni
una verificación del despliegue vivo. Los ensayos anteriores citados por los
trackers conservan su fecha y alcance; no se presentan como ejecutados ahora.
No se leyeron configuraciones privadas, claves ni secretos.

Verificación de esta entrega documental: 73 destinos de enlaces locales
comprobados en los cuatro documentos enlazados de entrada; siete IDs nuevos
únicos y sin cierres duplicados. `scripts/verify.sh` terminó en **OK (7/7)**,
incluidos tests Go con `-race`, fuzz, linters disponibles, reconstrucción de
consola, lógica de páginas y build del sitio. El primer intento agotó la cuota
temporal; el segundo encontró sockets prohibidos por el sandbox. La ejecución
final usó un temporal propio en el workspace, paralelismo limitado y permiso
para sockets locales. Esto verifica la compatibilidad del checkout, no los
recorridos propuestos ni el despliegue público.

Desfases corregidos en esta entrega: la introducción del README describía el
login nativo como futuro; `ID-001/008` aún mencionaban recuperación por bearer
aunque `people.go` la prohíbe; el encabezado de `deploy-product` decía que el
endpoint estaba en progreso mientras su aceptación posterior lo daba por
cerrado. Quedan textos históricos en otros contratos y el quickstart sigue
sin mostrar identidad. La antigüedad de esos textos no reabre funciones
cerradas ni demuestra el estado actual de producción.
