# ADR-005: PixiJS como renderer del cliente

Propósito: fijar PixiJS 8 como motor de render del mundo isométrico en `apps/web` y establecer que su ciclo de vida vive fuera del árbol de React.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-002](ADR-002-authoritative-server.md), [ADR-006](ADR-006-websocket-protocol.md), [ADR-011](ADR-011-movement-timed-polyline.md), [ADR-008](ADR-008-grid-coordinate-system.md)

---

## Contexto

`apps/web` es una aplicación Next.js 15 (App Router) con React 19 y TypeScript. Tiene dos responsabilidades muy distintas conviviendo en la misma página:

1. **Interfaz de usuario convencional**: paneles, menús, listas de unidades, diálogos, autenticación. Es trabajo para el que React es la herramienta correcta, y no se discute.
2. **La vista del mundo**: un grid isométrico continuo con unidades que se desplazan de forma fluida, actualizado por deltas que llegan por WebSocket.

La segunda responsabilidad es la que motiva este ADR. Sus requisitos concretos:

- **Proyección isométrica calculada íntegramente en el cliente.** El servidor nunca maneja píxeles: razona en tiles con coordenadas lógicas `x`, `y` de tipo `int32`. La conversión es responsabilidad exclusiva del cliente:

  ```
  screenX = (x - y) * (TILE_W / 2)      TILE_W = 64
  screenY = (x + y) * (TILE_H / 2)      TILE_H = 32
  ```

  ```
        tile (0,0)
            ╱╲              El eje X crece hacia el sureste en pantalla,
      (1,0)╱  ╲(0,1)        el eje Y hacia el suroeste.
          ╲    ╱            Un tile ocupa un rombo de 64 × 32 px.
           ╲  ╱             La profundidad de dibujo se ordena por (x + y).
            ╲╱
        tile (1,1)
  ```

- **Volumen de elementos en pantalla.** El área de interés por defecto es de `EO_INTEREST_RADIUS_CHUNKS=2` chunks alrededor del centro de vista, con chunks de 32 × 32 tiles: hasta 5 × 5 chunks, es decir **160 × 160 = 25 600 tiles** en el conjunto suscrito, más las entidades. Aunque el *viewport* real recorte una fracción de eso, el orden de magnitud descarta cualquier enfoque que cree un objeto pesado por tile.
- **Interpolación visual sub-tile.** El servidor razona en tiles y entrega la polilínea temporizada `{x, y, tMs}`; la interpolación entre waypoints es **exclusivamente visual y del cliente** ([ADR-011](ADR-011-movement-timed-polyline.md)). Eso implica recalcular la posición en pantalla de cada unidad en movimiento **en cada frame**, a 60 fps, con independencia del tick de 10 Hz del servidor.
- **Dos frecuencias desacopladas.** El servidor emite deltas a 10 Hz; la pantalla se refresca a ~60 Hz. El renderer necesita su propio bucle, alimentado por el estado que llega, no dirigido por él.
- **El cliente no simula.** No hay predicción ni reconciliación en el MVP ([ADR-002](ADR-002-authoritative-server.md)): el renderer dibuja lo que el servidor dice, interpolando para que se vea continuo. Esto reduce el problema a presentación pura, que es una buena noticia para la elección de herramienta.
- **Interacción con el mundo.** Clic sobre un tile para emitir `unit.move`, selección de unidades, arrastre de cámara y zoom. Requiere convertir de coordenadas de pantalla a coordenadas lógicas (la inversa de la proyección) y hacer *hit testing* sobre rombos, no sobre rectángulos.

## Decisión

**El mundo se dibuja con PixiJS 8 sobre WebGL.**

Alcance concreto:

- PixiJS 8 se usa **solo** para la vista del mundo: terreno, entidades, efectos y selección. Todo lo demás —paneles, menús, HUD, diálogos, formularios— es React sobre DOM.
- **El ciclo de vida de la aplicación Pixi queda fuera del árbol de React.** Se crea una vez, se monta sobre un `<canvas>` cuyo contenedor React solo posee como caja, y se destruye al desmontar. React no re-renderiza la escena, no la reconcilia y no la posee.
- La comunicación entre ambos mundos es explícita y unidireccional en cada sentido:
  - **Red → escena**: los deltas del WebSocket (`world.snapshot`, `entity.spawn`, `entity.update`, `entity.despawn`, `unit.movement.*`) actualizan un almacén de estado de cliente; una capa puente aplica esos cambios a los objetos de la escena Pixi mediante llamadas imperativas.
  - **Escena → React**: los eventos de interacción (selección, clic en tile) se publican hacia el estado de React mediante callbacks, para que el HUD reaccione.
- La proyección isométrica y su inversa viven en un módulo puro y testeable con Vitest, sin dependencia de Pixi. Es la pieza con más probabilidad de tener bugs sutiles y la más barata de probar aislada.
- El bucle de render es el *ticker* de Pixi. En cada frame recalcula la posición interpolada de las unidades en movimiento a partir de su polilínea y del tiempo transcurrido; **no** espera al siguiente delta del servidor para mover nada.

### Cómo se separa Pixi de React

```tsx
// Ilustrativo. El contenedor React posee la caja; Pixi posee la escena.
function WorldCanvas({ store }: { store: WorldStore }) {
  const hostRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    const scene = new WorldScene();          // objeto plano, no componente

    scene.init(hostRef.current!).then(() => {
      if (cancelled) { scene.destroy(); return; }
      scene.bind(store);                     // suscripción imperativa a los deltas
    });

    return () => { cancelled = true; scene.destroy(); };
  }, []);                                    // se monta una vez; nunca se re-renderiza

  return <div ref={hostRef} className="world-canvas-host" />;
}
```

Las reglas que hacen que esto funcione:

1. El `useEffect` tiene lista de dependencias vacía. Si alguna prop cambiante entrase en ella, la escena se destruiría y recrearía, lo que es catastrófico en coste.
2. Ningún objeto de la escena se guarda en `useState`. Cambiarlo dispararía renders de React sin ninguna utilidad.
3. Los datos que cambian a alta frecuencia (posiciones interpoladas) **nunca** pasan por el estado de React. Van directos del almacén a la escena.
4. Se contempla el doble montaje del *Strict Mode* de React 19 en desarrollo: la función de limpieza destruye la escena de verdad, y la bandera `cancelled` evita que una inicialización asíncrona en curso deje una aplicación Pixi huérfana consumiendo un contexto WebGL.
5. Next.js con App Router renderiza en servidor por defecto; el componente de la vista se carga solo en cliente, porque no hay `window` ni WebGL durante el render del servidor.

## Alternativas consideradas

### A. Canvas 2D a mano

**Ventajas reales.** Cero dependencias y control absoluto: se escribe exactamente lo que se dibuja, sin capas intermedias que entender ni depurar. La API `CanvasRenderingContext2D` es estable, universalmente soportada y fácil de razonar. Para tilemaps isométricos hay implementaciones clásicas muy eficientes que aprovechan que el terreno estático se puede pre-renderizar a un canvas fuera de pantalla y volcarlo de una pieza. Es, además, la opción con menor tamaño de bundle, algo nada trivial en una aplicación web.

**Por qué se descarta.** El coste está en todo lo que habría que escribir *igualmente*: batching de sprites, atlas de texturas, gestión de la caché, ordenación por profundidad, contenedores con transformaciones anidadas, *hit testing*, capas y cámara. Es reimplementar el 60 % de PixiJS con menos pruebas de campo. Y aun haciéndolo bien, Canvas 2D no aprovecha la GPU para el caso que más lo necesita: miles de sprites individuales con transformaciones propias. Con terreno estático el rendimiento sería aceptable; con muchas unidades en movimiento simultáneo, no. El ahorro en dependencias se paga con creces en código propio que hay que mantener y probar.

### B. Phaser

**Ventajas reales, y es la alternativa más seria.** Phaser es un framework de juego completo: escenas, sistema de entrada, animaciones de sprites, tweens, física, gestión de assets, cámaras con seguimiento y hasta soporte específico para tilemaps isométricos. Buena parte del trabajo que aquí habrá que escribir a mano viene resuelto. La comunidad es enorme, la documentación excelente y hay ejemplos directos del caso de uso. Para un equipo que quiera resultados rápidos, es una elección defendible y probablemente más veloz en las primeras semanas.

**Por qué se descarta.** Phaser quiere ser el dueño del ciclo de vida de la aplicación: su bucle de juego, su gestor de escenas y su gestor de estado están pensados para que el juego *sea* la aplicación. Aquí el juego es **una vista dentro de una aplicación Next.js** que también tiene enrutado, autenticación, HUD en React y un cliente WebSocket que no le pertenece. Integrar dos sistemas que quieren dirigir el ciclo de vida genera fricción constante. Además, gran parte de lo que aporta —física, animaciones, tweens, entrada de teclado para plataformas— no se usa: el estado lo dicta el servidor y la única "animación" es interpolar entre waypoints conocidos. Se pagaría el peso y la opinión del framework a cambio de utilidades que el modelo autoritativo hace innecesarias.

### C. Three.js con cámara ortográfica

**Ventajas reales.** Una cámara ortográfica en un motor 3D produce isometría **auténtica**, no simulada: no hace falta calcular la proyección a mano ni ordenar por profundidad, porque el *depth buffer* lo resuelve. Abre la puerta a elevación real del terreno, iluminación, sombras y transiciones de cámara que en 2D son costosas o imposibles. Three.js es maduro, muy mantenido y con una comunidad enorme.

**Por qué se descarta.** El mundo de Empires Online es **2D por definición**: un grid de tiles cuadrados con coordenadas `int32` y una proyección fijada por fórmula en el canon (`TILE_W = 64`, `TILE_H = 32`). No hay elevación en el modelo de datos: `HILL` y `MOUNTAIN` son tipos de terreno con multiplicador de coste, no alturas. Adoptar un motor 3D obligaría a modelar en 3D algo que es 2D, con el coste conceptual añadido de mallas, materiales, cámaras y luces, más assets que habría que producir en otro formato. La carga cognitiva para el equipo es notablemente mayor y el beneficio, para el MVP, es nulo. Sería la elección correcta si el mundo tuviera relieve real; no lo tiene.

### D. DOM (elementos HTML posicionados con CSS)

**Ventajas reales.** Es la opción con integración perfecta con React: cada entidad es un componente, las herramientas de desarrollo del navegador inspeccionan la escena elemento a elemento, la accesibilidad y la selección de texto funcionan sin esfuerzo y las transiciones CSS podrían encargarse de la interpolación con aceleración por GPU y sin escribir un bucle. Para un prototipo con pocas decenas de entidades es sorprendentemente eficaz.

**Por qué se descarta.** No escala. Miles de nodos DOM con `transform` actualizados a 60 fps saturan el hilo principal en recálculo de estilo, layout y composición mucho antes de llegar a las cifras del área de interés. La ordenación por profundidad exigiría manipular `z-index` de forma continua, algo que el navegador no está optimizado para soportar. El *hit testing* sobre rombos es incompatible con las cajas rectangulares del DOM sin recurrir a máscaras o a comprobación manual, lo que anula la ventaja de integración. Es la alternativa más cómoda y la que peor resuelve el problema real.

### E. Comparativa

| Criterio | PixiJS 8 | Canvas 2D | Phaser | Three.js orto | DOM |
|---|---|---|---|---|---|
| Rendimiento con miles de sprites | Excelente (WebGL + batching) | Medio | Excelente | Excelente | Malo |
| Encaje con isometría 2D | Muy bueno | Muy bueno | Muy bueno | Forzado | Regular |
| Convivencia con React/Next.js | Buena (con separación) | Buena | Difícil | Buena | Nativa |
| Trabajo propio a escribir | Medio | **Alto** | Bajo | Medio | Bajo |
| Peso y complejidad añadida | Media | Nula | Alta | Alta | Nula |
| Control fino de la escena | **Alto** | Total | Medio (opinado) | Alto | Bajo |
| Utilidad de lo que no se usa | Poca | — | Mucha (física, tweens) | Mucha (3D) | — |

## Consecuencias

### Positivas

- **Rendimiento adecuado por construcción.** WebGL con batching automático dibuja miles de sprites que comparten atlas de textura en muy pocas llamadas de dibujo, que es exactamente el perfil de un tilemap isométrico con unidades encima.
- **Control fino de la escena.** El grafo de `Container` permite estructurar el mundo en capas explícitas —terreno, capa de ocupación, entidades, selección, superposiciones de depuración— y aplicar la cámara como una transformación sobre el contenedor raíz, sin tocar cada objeto.
- **La ordenación por profundidad es directa.** Al ser la escena propia, ordenar los sprites por `(x + y)` para el apilado isométrico es una operación explícita y controlable, no un efecto secundario del motor.
- **Encaje limpio con el modelo autoritativo.** Como el cliente no simula, la escena es una función del estado recibido más el tiempo transcurrido. Eso hace que la interpolación entre waypoints sea un cálculo puro por frame, fácil de aislar y de probar.
- **Se aprovecha React donde React es bueno.** El HUD, los paneles y los diálogos siguen siendo componentes normales, con su propio ciclo de re-render, sin competir con el bucle de 60 fps.
- **PixiJS 8 no impone estructura de juego.** Es una biblioteca de render, no un framework: no hay gestor de escenas ni bucle de juego que negociar con Next.js.

### Negativas

- **El ciclo de vida de Pixi debe quedar fuera del árbol de React, y esto es una fuente permanente de errores.** Es el coste central de la decisión. Los modos de fallo son concretos y ninguno es hipotético: recrear la aplicación Pixi al re-renderizar el componente contenedor; fugar contextos WebGL por no destruir la escena en la limpieza del efecto; dejar una aplicación huérfana por el doble montaje del *Strict Mode*; guardar objetos de la escena en estado de React y provocar renders inútiles. Cada uno exige una convención explícita y revisión activa.
- **El equipo asume el coste de gestionar la escena manualmente.** No hay reconciliación: crear, actualizar y destruir sprites al ritmo de `entity.spawn`, `entity.update` y `entity.despawn` es código imperativo escrito a mano, con su propia contabilidad de qué existe y qué no. Un sprite que no se destruye al recibir `entity.despawn` es una fuga de memoria silenciosa.
- **Dos modelos mentales conviviendo.** Un desarrollador que toque la vista del mundo pasa de declarativo a imperativo al cruzar la frontera del `<canvas>`. Es carga cognitiva real y hay que documentarla en la guía de contribución.
- **La depuración es más pobre.** Dentro del canvas no hay inspector de elementos: hay que construir superposiciones de depuración (rejilla de tiles, límites de chunk, coordenadas lógicas bajo el cursor) para poder trabajar. Ese utillaje es trabajo adicional que conviene planificar desde el principio.
- **Dependencia de WebGL.** Un navegador sin WebGL disponible, o con la aceleración deshabilitada, degrada mucho la experiencia. La estrategia de respaldo es **TBD (fuera de MVP)**; como mínimo debe detectarse la ausencia de contexto y mostrar un mensaje claro en lugar de un canvas negro.
- **Assets y atlas de texturas.** Aprovechar el batching exige empaquetar los sprites en atlas; eso introduce un paso de preparación de assets y una convención de nombres que hoy no existe. El pipeline concreto es **TBD (fuera de MVP)**.
- **Peso en el bundle** frente a Canvas 2D, en una aplicación que además carga Next.js y React.

### Neutras

- La proyección isométrica y su inversa quedan como un módulo puro de TypeScript, probado con Vitest de forma independiente del renderer. Es la parte del cliente con mayor densidad de bugs sutiles y la más barata de cubrir con tests.
- El componente de la vista del mundo se carga solo en cliente; el resto de `apps/web` conserva las capacidades de renderizado en servidor del App Router.
- La cámara (posición y zoom) es estado del cliente. Cuando su desplazamiento cambia el chunk central, se emite `session.view` hacia el servidor, sujeto a rate limit ([ADR-008](ADR-008-grid-coordinate-system.md)). El renderer no decide qué entidades existen, solo cómo se ven las que el servidor ha enviado.
- Las animaciones y efectos que no alteran estado (resaltado de selección, marcador de destino tras un clic) son responsabilidad exclusiva del cliente y no requieren confirmación del servidor. No son estado del juego.

## Estado

**Aceptado** el 2026-09-09.

Se reevaluaría si el modelo del mundo incorporase elevación real del terreno o iluminación dinámica, en cuyo caso Three.js con cámara ortográfica pasaría a ser la opción natural; o si el perfilado del cliente mostrase que el cuello de botella está en el número de llamadas de dibujo pese a un uso correcto de atlas, lo que apuntaría a un problema de organización de assets antes que a un problema de biblioteca.
