// 左侧分支，同时引用一个只用于类型的声明。
import { shared } from "./shared.ts";
import type { Shared } from "./types.ts";

export const a: Shared = shared + 1;
