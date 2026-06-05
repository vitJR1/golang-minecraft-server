<p align="center">
  <img src="server-icon.png" width="200" alt="Nest Logo" />
</p>

Расстояние между игроками
dx := a.X - b.X
dy := a.Y - b.Y
dz := a.Z - b.Z

distSq := dx*dx + dy*dy + dz*dz

Главное — не считать sqrt. Сравнивай квадрат расстояния:

minDistance := radius*radius

if distSq <= minDistance {
// рядом
}