import { createRequestQueue } from './requestQueue'

// Emby 媒体参数保留两个并发名额，不再与 TMDB 播出信息共用。
export const queueEmbyRequest = createRequestQueue(2)
