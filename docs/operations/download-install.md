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

Descarga el archivo desde el canal autorizado de la release y obtén su checksum
por una fuente confiable. Verificar un checksum contra un valor que vino de
un origen comprometido no demuestra autenticidad; no se afirma firma de release.

## Linux: instalar un binario nuevo sin privilegios

El helper acepta una descarga local y el SHA esperado. No hace red, no ejecuta
scripts del archivo, no instala servicios y no sobrescribe un binario existente:

```sh
mkdir -p "$HOME/.local/bin"
scripts/release/install.sh ./switchboard_VERSION_linux_amd64.tar.gz \
  SHA256_ESPERADO "$HOME/.local/bin/switchboard"
"$HOME/.local/bin/switchboard" version
"$HOME/.local/bin/switchboard" setup -dir ./my-edge
```

Reemplaza versión y checksum por los valores de la release elegida. El destino
debe ser un archivo nuevo dentro de un directorio que controles. El helper
verifica una copia privada del archivo y extrae solo el miembro regular
`switchboard`, rechaza duplicados/enlaces y usa un enlace de destino exacto
que también se niega a una creación concurrente. Su implementación es Linux.

Para un servicio existente usa [upgrade](systemd.md), no elimines el binario
actual para forzar al instalador. El helper necesita Bash, tar y sha256sum
(o shasum). El servicio no necesita esas herramientas para ejecutar el gateway.

## Linux: instalar el servicio de una vez

`scripts/release/install-service.sh` hace la instalación documentada en
[systemd](systemd.md) en un solo paso: crea la cuenta `switchboard`, instala el
binario y la config como `root`, genera un token de administración aleatorio
(legible solo por root y la cuenta del servicio), migra el estado, inicializa
el archivo operativo, habilita el servicio y espera a que la consola responda.
Todo queda en loopback; no toca DNS, firewall ni TLS público, y se niega a
sobrescribir una instalación existente (para eso está `upgrade`).

```sh
sudo scripts/release/install-service.sh --dry-run --binary ./switchboard   # leer el plan
sudo scripts/release/install-service.sh --binary ./switchboard --target http://127.0.0.1:3000
# o, con una descarga y su checksum de un canal confiable:
sudo scripts/release/install-service.sh --archive ./switchboard_VERSION_linux_amd64.tar.gz --sha256 SHA256_ESPERADO
```

Al terminar imprime los pasos siguientes: túnel SSH a la consola, enlace de un
solo uso (`console login`) y alta de la primera aplicación en la consola.
`scripts/release/test-install-service.sh` lo prueba con el binario real en un
directorio de staging, sin root.

## macOS y Windows

En macOS, verifica el tar.gz con `shasum -a 256`, extrae el binario de la release
en un directorio propio y comprueba `switchboard version`. El helper Linux se
niega a ejecutarse en otros sistemas. No hay instalador launchd aprobado.

En Windows, verifica el ZIP con `Get-FileHash -Algorithm SHA256`, usa
`Expand-Archive` en un destino nuevo y ejecuta `switchboard.exe version`.
Los comandos de configuración/validación y el proxy son portables; la gestión
systemd y el socket Unix administrativo tienen un alcance distinto.

## Instalar un servicio y conservar el estado

La [guía systemd](systemd.md) define binario/config root-owned, usuario de servicio,
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
