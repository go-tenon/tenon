// 上传：按客户端协议把文件直传到对象存储，文件不经过应用服务器。

import { type AbortSignalLike, abortError, type BlobLike, type FetchLike, globalFetch } from "./fetch"

/** 对象存储的签名请求，与服务端 filesystem.SignedRequest 的 JSON 相同。 */
export interface SignedRequest {
  url: string
  method: string
  headers?: Record<string, readonly string[] | string>
}

/** 分片上传的会话，与服务端 filesystem.Multipart 的 JSON 相同：除最后一片外，每片正好 part_size 字节。 */
export interface Multipart {
  upload_id: string
  part_size: number
  part_count: number
}

/** 已经上传的一片，合并时交给服务端。 */
export interface UploadedPart {
  number: number
  etag: string
}

/**
 * 直传的各个步骤，通常是生成客户端的方法，因为接口的路径由应用决定：
 *
 *	{
 *	  prepare: (input) => api.file.prepare(input),
 *	  sign: (id) => api.file.sign(id),
 *	  multipart: {
 *	    create: (id) => api.file.multipart(id),
 *	    signPart: (id, uploadId, n) => api.file.signPart(id, uploadId, n),
 *	    complete: (id, uploadId, parts) => api.file.completeMultipart(id, uploadId, { parts }),
 *	    abort: (id, uploadId) => api.file.abortMultipart(id, uploadId),
 *	  },
 *	}
 */
export interface UploadSteps<F extends { id: string }> {
  /** 登记文件，返回 temporary 状态的文件。 */
  prepare(input: { filename: string; content_type?: string; size: number }): Promise<F>
  /** 为单次上传签名。 */
  sign(id: string): Promise<SignedRequest>
  /** 分片上传的步骤；不提供时总是单次上传。 */
  multipart?: {
    create(id: string): Promise<Multipart>
    signPart(id: string, uploadId: string, number: number): Promise<SignedRequest>
    complete(id: string, uploadId: string, parts: UploadedPart[]): Promise<F>
    abort(id: string, uploadId: string): Promise<unknown>
  }
}

/** uploadFile 的选项。 */
export interface UploadOptions {
  /** 发送到对象存储的 fetch，默认是全局的 fetch。签名的地址已经带有凭据，不需要 Authorization。 */
  fetch?: FetchLike
  /** 取消上传：未完成的分片会话随之放弃。 */
  signal?: AbortSignalLike
  /** 进度：单次上传在完成时调用一次，分片上传每完成一片调用一次。 */
  onProgress?: (progress: { loaded: number; total: number }) => void
  /** 文件大于这个字节数时分片上传，默认 64 MiB。 */
  multipartThreshold?: number
  /** 同时上传的分片数，默认 4。 */
  concurrency?: number
  /** 一片上传失败后重新签名并重传的次数，默认 2；仍然失败时放弃分片会话。 */
  partRetries?: number
}

/** 对象存储拒绝上传时抛出的错误。 */
export class UploadError extends Error {
  constructor(
    readonly status: number,
    readonly part?: number,
  ) {
    super(part === undefined ? `upload failed: HTTP ${status}` : `upload of part ${part} failed: HTTP ${status}`)
    this.name = "UploadError"
  }
}

/**
 * 把 file 直传到对象存储，返回登记的文件（仍是 temporary 状态，交给业务接口确认后才保留）。
 * 小文件签名一次，用一个请求上传；大于 multipartThreshold 且提供了 multipart 时分片上传，记下每片的 ETag，最后合并。
 * 一片失败时重新签名并重传，仍然失败或被取消时放弃分片会话。对象存储的 CORS 要允许 PUT 和请求头
 * Content-Type、If-None-Match，并暴露 ETag 响应头。
 */
export async function uploadFile<F extends { id: string }>(
  file: BlobLike,
  steps: UploadSteps<F>,
  options: UploadOptions = {},
): Promise<F> {
  const send = options.fetch ?? globalFetch()
  const { signal } = options
  const total = file.size
  const prepared = await steps.prepare({
    filename: file.name ?? "file",
    content_type: file.type || undefined,
    size: total,
  })
  if (signal?.aborted) throw abortError()

  const put = async (req: SignedRequest, body: unknown, part?: number) => {
    const res = await send(req.url, { method: req.method, headers: flatten(req.headers), body, signal })
    if (!res.ok) throw new UploadError(res.status, part)
    return res
  }

  if (!steps.multipart || total <= (options.multipartThreshold ?? 64 << 20)) {
    await put(await steps.sign(prepared.id), file)
    options.onProgress?.({ loaded: total, total })
    return prepared
  }

  const mp = steps.multipart
  const session = await mp.create(prepared.id)
  try {
    const parts: UploadedPart[] = []
    let loaded = 0
    let next = 1
    let failed = false
    const worker = async () => {
      while (next <= session.part_count && !failed) {
        if (signal?.aborted) throw abortError()
        const number = next++
        const start = (number - 1) * session.part_size
        const end = Math.min(start + session.part_size, total)
        let res: Awaited<ReturnType<typeof put>> | undefined
        for (let attempt = 0; !res; attempt++) {
          try {
            res = await put(await mp.signPart(prepared.id, session.upload_id, number), file.slice(start, end), number)
          } catch (err) {
            if (signal?.aborted || attempt >= (options.partRetries ?? 2)) throw err
          }
        }
        const etag = res.headers.get("ETag")
        if (!etag)
          throw new Error(
            "upload: the object storage did not expose the ETag header; allow it in the bucket CORS settings",
          )
        parts.push({ number, etag })
        loaded += end - start
        options.onProgress?.({ loaded, total })
      }
    }
    const run = () =>
      worker().catch((err) => {
        failed = true // 其他分片不再开始
        throw err
      })
    await Promise.all(Array.from({ length: Math.min(options.concurrency ?? 4, session.part_count) }, run))
    parts.sort((a, b) => a.number - b.number)
    return await mp.complete(prepared.id, session.upload_id, parts)
  } catch (err) {
    await mp.abort(prepared.id, session.upload_id).catch(() => {})
    throw err
  }
}

/** 把签名的请求头（值可能是数组）转为普通对象。 */
function flatten(headers: SignedRequest["headers"]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(headers ?? {})) out[k] = Array.isArray(v) ? v.join(", ") : String(v)
  return out
}
