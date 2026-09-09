#!/usr/bin/env node
/**
 * Compila el Game Server dejando el binario con el nombre correcto en cada
 * sistema operativo.
 *
 * Existe por un fallo concreto y desagradable: `go build -o bin/empires-server`
 * en Windows escribe un archivo SIN extensión, mientras que lo que se acaba
 * ejecutando es `bin/empires-server.exe`. Si alguna vez hubo un `.exe` en esa
 * carpeta, se queda ahí, y a partir de entonces cada compilación produce un
 * binario nuevo que nadie ejecuta mientras se sigue lanzando el viejo. El
 * síntoma no señala la causa: el código recién escrito «no hace efecto», y se
 * busca el error en el código.
 *
 * `go build -o bin/` tampoco vale: nombra el binario según el directorio del
 * paquete (`cmd/server` -> `server`), y el artefacto tiene que llamarse
 * `empires-server` porque así lo referencian el Dockerfile y la operación.
 *
 * Además borra cualquier binario hermano obsoleto, para que no pueda repetirse.
 */
import { execFileSync } from 'node:child_process';
import { existsSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const moduleDir = join(repoRoot, 'services', 'game-server');
const binDir = join(moduleDir, 'bin');

const targets = [
  { pkg: './cmd/server', name: 'empires-server' },
  { pkg: './cmd/migrate', name: 'migrate' },
];

// La extensión la dice el propio toolchain, no una suposición sobre el sistema:
// así también acierta al compilar cruzado con GOOS.
const goexe = execFileSync('go', ['env', 'GOEXE'], { cwd: moduleDir, encoding: 'utf8' }).trim();

for (const { pkg, name } of targets) {
  // Un hermano con la extensión equivocada es exactamente la trampa que este
  // script evita: se retira antes de compilar.
  for (const variante of [name, `${name}.exe`]) {
    const ruta = join(binDir, variante);
    if (variante !== name + goexe && existsSync(ruta)) {
      rmSync(ruta, { force: true });
      console.log(`retirado binario obsoleto: bin/${variante}`);
    }
  }

  const out = join(binDir, name + goexe);
  execFileSync('go', ['build', '-o', out, pkg], { cwd: moduleDir, stdio: 'inherit' });
  console.log(`compilado bin/${name}${goexe}`);
}
