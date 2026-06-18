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

function New-RoundedRectPath([single]$x, [single]$y, [single]$w, [single]$h, [single]$r) {
  $path = New-Object System.Drawing.Drawing2D.GraphicsPath
  $d = $r * 2
  $path.AddArc($x, $y, $d, $d, 180, 90)
  $path.AddArc($x + $w - $d, $y, $d, $d, 270, 90)
  $path.AddArc($x + $w - $d, $y + $h - $d, $d, $d, 0, 90)
  $path.AddArc($x, $y + $h - $d, $d, $d, 90, 90)
  $path.CloseFigure()
  return $path
}

function New-IconPng([int]$size) {
  $bmp = New-Object System.Drawing.Bitmap $size, $size, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
  $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
  $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
  $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
  $g.Clear([System.Drawing.Color]::Transparent)

  $pad = [single]($size * 0.07)
  $rect = [System.Drawing.RectangleF]::new($pad, $pad, $size - ($pad * 2), $size - ($pad * 2))

  if ($size -ge 32) {
    $shadowRect = [System.Drawing.RectangleF]::new($pad, $pad + ($size * 0.025), $size - ($pad * 2), $size - ($pad * 2))
    $shadowBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(42, 30, 91, 124))
    $g.FillEllipse($shadowBrush, $shadowRect)
    $shadowBrush.Dispose()
  }

  $top = [System.Drawing.Color]::FromArgb(255, 68, 186, 235)
  $bottom = [System.Drawing.Color]::FromArgb(255, 34, 158, 217)
  $brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush $rect, $top, $bottom, ([System.Drawing.Drawing2D.LinearGradientMode]::Vertical)
  $g.FillEllipse($brush, $rect)
  $brush.Dispose()

  if ($size -ge 48) {
    $shine = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(45, 255, 255, 255))
    $g.FillEllipse($shine, [System.Drawing.RectangleF]::new($size * 0.24, $size * 0.18, $size * 0.42, $size * 0.22))
    $shine.Dispose()
  }

  $plane = New-Object System.Drawing.Drawing2D.GraphicsPath
  $plane.AddPolygon([System.Drawing.PointF[]]@(
    [System.Drawing.PointF]::new($size * 0.235, $size * 0.505),
    [System.Drawing.PointF]::new($size * 0.755, $size * 0.275),
    [System.Drawing.PointF]::new($size * 0.590, $size * 0.725),
    [System.Drawing.PointF]::new($size * 0.485, $size * 0.555),
    [System.Drawing.PointF]::new($size * 0.370, $size * 0.662)
  ))
  $white = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(255, 255, 255, 255))
  $g.FillPath($white, $plane)

  if ($size -ge 64) {
    $crease = New-Object System.Drawing.Drawing2D.GraphicsPath
    $crease.AddPolygon([System.Drawing.PointF[]]@(
      [System.Drawing.PointF]::new($size * 0.485, $size * 0.555),
      [System.Drawing.PointF]::new($size * 0.755, $size * 0.275),
      [System.Drawing.PointF]::new($size * 0.430, $size * 0.600)
    ))
    $creaseBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(52, 34, 158, 217))
    $g.FillPath($creaseBrush, $crease)
    $creaseBrush.Dispose()
    $crease.Dispose()
  }

  if ($size -ge 48) {
    $shield = New-Object System.Drawing.Drawing2D.GraphicsPath
    $shield.AddPolygon([System.Drawing.PointF[]]@(
      [System.Drawing.PointF]::new($size * 0.625, $size * 0.600),
      [System.Drawing.PointF]::new($size * 0.765, $size * 0.550),
      [System.Drawing.PointF]::new($size * 0.905, $size * 0.600),
      [System.Drawing.PointF]::new($size * 0.875, $size * 0.790),
      [System.Drawing.PointF]::new($size * 0.765, $size * 0.885),
      [System.Drawing.PointF]::new($size * 0.655, $size * 0.790)
    ))
    $shieldBrush = New-Object System.Drawing.SolidBrush ([System.Drawing.Color]::FromArgb(245, 17, 115, 164))
    $g.FillPath($shieldBrush, $shield)
    $shieldBrush.Dispose()

    $pen = New-Object System.Drawing.Pen ([System.Drawing.Color]::White), ([single]([Math]::Max(2, $size * 0.030)))
    $pen.StartCap = [System.Drawing.Drawing2D.LineCap]::Round
    $pen.EndCap = [System.Drawing.Drawing2D.LineCap]::Round
    $pen.LineJoin = [System.Drawing.Drawing2D.LineJoin]::Round
    $g.DrawLines($pen, [System.Drawing.PointF[]]@(
      [System.Drawing.PointF]::new($size * 0.705, $size * 0.715),
      [System.Drawing.PointF]::new($size * 0.755, $size * 0.765),
      [System.Drawing.PointF]::new($size * 0.835, $size * 0.665)
    ))
    $pen.Dispose()
    $shield.Dispose()
  }

  $white.Dispose()
  $plane.Dispose()

  $ms = New-Object System.IO.MemoryStream
  $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  if ($size -eq 256) {
    $bmp.Save($previewPath, [System.Drawing.Imaging.ImageFormat]::Png)
  }
  $g.Dispose()
  $bmp.Dispose()
  return ,$ms.ToArray()
}

$sizes = @(256, 128, 64, 48, 32, 16)
$images = @()
foreach ($size in $sizes) {
  $images += [pscustomobject]@{
    Size = $size
    Data = New-IconPng $size
  }
}

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

Write-Host "Generated $outputPath"
