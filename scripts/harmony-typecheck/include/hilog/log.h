/* hilog/log.h —— 桌面类型检查用的最小替身 (不是真实现)。
   只提供 gfx/harmony 用到的那几个符号, 用于在无 OHOS SDK 的机器上对鸿蒙 Go
   侧做编译期类型检查。不参与任何真实构建。 */
#ifndef GOHARMONY_HILOG_STUB_H
#define GOHARMONY_HILOG_STUB_H

typedef enum { LOG_APP = 0 } LogType;
typedef enum {
  LOG_DEBUG = 3, LOG_INFO = 4, LOG_WARN = 5, LOG_ERROR = 6, LOG_FATAL = 7
} LogLevel;

int OH_LOG_Print(LogType type, LogLevel level, unsigned int domain,
                 const char *tag, const char *fmt, ...);

#ifndef LOG_DOMAIN
#define LOG_DOMAIN 0
#endif
#ifndef LOG_TAG
#define LOG_TAG "Gox"
#endif
#define OH_LOG_INFO(type, ...) ((void)0)

#endif
