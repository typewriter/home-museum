module.exports = {
  chainWebpack: config => {
    config
      .plugin('html')
      .tap(args => {
        args[0].title = 'Home museum (β) - 画面いっぱいに美術作品を楽しもう'
        return args
      })
  }
}