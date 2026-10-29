# Descargar, instalar y actualizar

`REL-012`, implementación local 26/10/2026. La distribución pública sigue siendo
una acción del mantenedor: este trabajo no cambia la visibilidad del repositorio
ni publica una release. Un enlace público al sitio del proyecto no garantiza
acceso a los assets de un repositorio privado.

## Elegir una versión

GoReleaser prepara Linux, macOS y Windows, en amd64 y arm64, con checksums SHA-256
y SBOM. Elige SO/arquitectura y lee las notas de **esa versión**. La alpha.1
histórica no incluye las funciones posteriores de main. Una compilación local
con cambios sin commit es candidata, no una release reproducible publicada.

Los ejemplos `ozal_VERSION_*` corresponden a los paquetes del nombre nuevo;
no afirman que ya exista una release Ozal publicada. Los tags anteriores
conservan sus archivos y binarios `switchboard`: usa las instrucciones y el
instalador de ese tag. Para actualizar una instalación anterior, sigue la
[migración a Ozal](administration.md#migrar-de-switchboard-a-ozal).

Descarga el archivo desde el canal autorizado de la release y obtén su checksum
por una fuente confiable. Verificar un checksum contra un valor que vino de
un origen comprometido no demuestra autenticidad; no se afirma firma de release.

## Linux: instalar un binario nuevo sin privilegios

La primera descarga se verifica antes de ejecutar código de la release. En un
directorio propio nuevo, verifica el archivo completo y extrae solamente el
binario regular `ozal`; inspecciona el listado si el canal no es confiable.
No ejecutes un instalador extraído antes de verificar el checksum del archivo.

```sh
sha256sum ./ozal_VERSION_linux_amd64.tar.gz  # comparar con SHA256_ESPERADO
mkdir ./ozal-release
# Después de verificar el archivo y su miembro regular:
tar -xzf ./ozal_VERSION_linux_amd64.tar.gz -C ./ozal-release ozal
./ozal-release/ozal version
```

Con un binario **actual ya confiable**, puedes instalar otra descarga local sin
Bash, tar ni sha256sum en el flujo de instalación:

```sh
mkdir -p "$HOME/.local/bin"
./ozal-release/ozal install binary -archive ./ozal_VERSION_linux_amd64.tar.gz \
  -sha256 SHA256_ESPERADO -destination "$HOME/.local/bin/ozal"  # plan
# Agregar -apply para verificar una copia privada e instalar.
```

El comando Go exige checksum confiable, un destino nuevo y exactamente un
miembro regular `ozal`; rechaza duplicados, enlaces, archivo corrupto y límites
excedidos. No sobrescribe ni ejecuta el binario extraído. Para un servicio
existente usa [upgrade](administration.md#servicio-y-propiedad-del-estado).
`scripts/release/install.sh` sólo traduce argumentos; necesita `OZAL_BIN` con
la ruta al binario confiable actual. No sirve como raíz de confianza.

## Linux: instalar el servicio de una vez

`ozal install service` hace la instalación documentada en
[systemd](administration.md#servicio-y-propiedad-del-estado) en un solo paso: crea la cuenta `ozal`, instala el
binario y la config como `root`, genera un token de administración aleatorio
(legible solo por root y la cuenta del servicio), migra el estado, inicializa
el archivo operativo, habilita el servicio y espera a que la consola responda.
Todo queda en loopback; no toca DNS, firewall ni TLS público, y se niega a
sobrescribir una instalación existente (para eso está `upgrade`).

```sh
sudo ./ozal install service -binary ./ozal   # leer el plan
sudo ./ozal install service -binary ./ozal -target http://127.0.0.1:3000 -apply
# o, con una descarga y su checksum de un canal confiable:
sudo ./ozal install service -archive ./ozal_VERSION_linux_amd64.tar.gz -sha256 SHA256_ESPERADO -apply
```

El instalador deja `/usr/local/bin/ozal` como enlace al binario instalado:
`ozal` se ejecuta desde cualquier directorio y `ozal upgrade`
sigue actualizándolo. Los comandos de administración usan
`/etc/ozal/config.json` cuando no hay un `config.json` en el directorio actual.

Al terminar imprime los pasos siguientes: túnel SSH a la consola, enlace de un
solo uso (`sudo ozal console enrol`, que crea al primer administrador con su passkey) y alta de la primera aplicación en la consola.
`scripts/release/test-install-service.sh` lo prueba con el binario real en un
directorio de staging, sin root. El comando nativo equivalente es
`ozal install service -binary ./ozal -root /tmp/ozal-stage -apply`: no crea
cuentas ni llama a systemd. Una instalación real requiere root y systemd; no se
ha ejecutado como parte de la aceptación local. Si falla después de crear
archivos, conserva el estado para inspección y no permite sobrescribirlo.

## macOS y Windows

En macOS, verifica el tar.gz con `shasum -a 256`, extrae el binario de la release
en un directorio propio y comprueba `ozal version`. La instalación del servicio se
niega a ejecutarse en otros sistemas. No hay instalador launchd aprobado.

En Windows, verifica el ZIP con `Get-FileHash -Algorithm SHA256`, usa
`Expand-Archive` en un destino nuevo y ejecuta `ozal.exe version`.
Los comandos de configuración/validación y el proxy son portables; la gestión
systemd y el socket Unix administrativo tienen un alcance distinto.

## Instalar un servicio y conservar el estado

La [guía systemd](administration.md#servicio-y-propiedad-del-estado) define binario/config root-owned, usuario de servicio,
estado privado y socket administrativo. Decide el dominio permanente, HTTPS,
proveedor o passkeys, transporte al origen y backup antes de mover un sitio real.
Sigue el recorrido de [primera aplicación](first-application.md).

`upgrade` recibe un hash que el operador ya verificó; valida, migra con backup,
activa y comprueba el admin, con reversión si falla. No descubre ni autentica
un canal por sí mismo. La migración 012 exige nuevo login OIDC; conserva datos
y credenciales nativas. Una sesión antigua sin método también requiere login. Prueba el restore en un destino descartable.

## Cuándo está cerrado el canal

Además de pruebas del instalador y paquetes locales, una persona sin acceso al
repo debe poder obtener la versión anunciada, verificarla e instalarla siguiendo
esta guía. Mientras el canal permanezca privado o no se haga ese ensayo, `REL-012`
conserva esa aceptación pendiente. No se cambia DNS, una release o la visibilidad
sin la autorización correspondiente.
