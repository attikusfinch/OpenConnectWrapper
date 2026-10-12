param(
  [string]$OutputIco = 'assets\app.ico',
  [string]$PreviewPng = 'assets\app-256.png'
)

$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..')

function Resolve-ProjectPath([string]$Path) {
  if ([System.IO.Path]::IsPathRooted($Path)) {
    return $Path
  }
  return Join-Path $root $Path
}

$outputPath = Resolve-ProjectPath $OutputIco
$previewPath = Resolve-ProjectPath $PreviewPng
New-Item -ItemType Directory -Force -Path (Split-Path $outputPath) | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path $previewPath) | Out-Null

Add-Type -AssemblyName System.Drawing

# Use the same master artwork for the window, taskbar and web interface.
$source = [System.Drawing.Image]::FromFile((Join-Path $root 'assets\logo.png'))
function New-IconPng([int]$size) {
  $bmp = New-Object System.Drawing.Bitmap $size, $size, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  try {
    $g.Clear([System.Drawing.Color]::Transparent)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.DrawImage($source, 0, 0, $size, $size)
    $ms = New-Object System.IO.MemoryStream
    try {
      $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
      if ($size -eq 256) {
        $bmp.Save($previewPath, [System.Drawing.Imaging.ImageFormat]::Png)
      }
      return ,$ms.ToArray()
    } finally { $ms.Dispose() }
  } finally {
    $g.Dispose()
    $bmp.Dispose()
  }
}

$sizes = @(256, 128, 64, 48, 32, 16)
$images = @()
foreach ($size in $sizes) {
  $images += [pscustomobject]@{
    Size = $size
    Data = New-IconPng $size
  }
}

$source.Dispose()
$fs = [System.IO.File]::Create($outputPath)
try {
  $writer = New-Object System.IO.BinaryWriter $fs
  $writer.Write([uint16]0)
  $writer.Write([uint16]1)
  $writer.Write([uint16]$images.Count)

  $offset = 6 + ($images.Count * 16)
  foreach ($image in $images) {
    $entrySize = $image.Size
    if ($entrySize -eq 256) {
      $entrySize = 0
    }
    $writer.Write([byte]$entrySize)
    $writer.Write([byte]$entrySize)
    $writer.Write([byte]0)
    $writer.Write([byte]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]32)
    $writer.Write([uint32]$image.Data.Length)
    $writer.Write([uint32]$offset)
    $offset += $image.Data.Length
  }

  foreach ($image in $images) {
    $writer.Write($image.Data)
  }
} finally {
  if ($writer) {
    $writer.Dispose()
  } else {
    $fs.Dispose()
  }
}

Copy-Item -LiteralPath $previewPath -Destination (Join-Path $root "internal\server\static\logo.png") -Force
Write-Host "Generated $outputPath"
