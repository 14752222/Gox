//go:build gpu

package gpu

import "math"

// putF32 把小端 float32 序列写进 b (顶点缓冲编码)。
func putF32(b []byte, fs ...float32) {
	for i, f := range fs {
		bits := math.Float32bits(f)
		b[i*4+0] = byte(bits)
		b[i*4+1] = byte(bits >> 8)
		b[i*4+2] = byte(bits >> 16)
		b[i*4+3] = byte(bits >> 24)
	}
}

// presentWGSL 是呈现路径唯一的着色器: 全屏四边形 + 帧缓冲纹理采样。
// 语义上等价于平台后端的 blit —— 逐像素拷贝, 因此 GPU 合成结果与 CPU
// 帧缓冲逐像素相等 (surface_test.go 对此有断言)。
//
// 后续阶段 (canvas 加速 / 全树 display list) 的着色器都挂在本文件旁边增长:
// 圆角矩形 SDF、线性渐变、阴影模糊、文字图集批次。
const presentWGSL = `
struct VertexInput {
    @location(0) position: vec2<f32>,
    @location(1) uv: vec2<f32>,
}
struct VertexOutput {
    @builtin(position) position: vec4<f32>,
    @location(0) uv: vec2<f32>,
}
@vertex
fn vs_main(in: VertexInput) -> VertexOutput {
    var out: VertexOutput;
    out.position = vec4<f32>(in.position, 0.0, 1.0);
    out.uv = in.uv;
    return out;
}
@group(0) @binding(0) var t_fb: texture_2d<f32>;
@group(0) @binding(1) var s_fb: sampler;
@fragment
fn fs_main(in: VertexOutput) -> @location(0) vec4<f32> {
    return textureSample(t_fb, s_fb, in.uv);
}
`
