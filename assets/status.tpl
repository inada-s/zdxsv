<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="utf-8">
<meta http-equiv="Refresh" content="60">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">
<link href="/assets/css/bootstrap.min.css" rel="stylesheet">
<!-- HTML5 shim and Respond.js for IE8 support of HTML5 elements and media queries -->
<!-- WARNING: Respond.js doesn't work if you view the page via file:// -->
<!--[if lt IE 9]>
  <script src="https://oss.maxcdn.com/html5shiv/3.7.3/html5shiv.min.js"></script>
  <script src="https://oss.maxcdn.com/respond/1.4.2/respond.min.js"></script>
<![endif]-->
<style>
body { padding-top: 70px; }
</style>

<title>ガンダムvs.Zガンダム エミュ鯖ステータス</title>
</head>

<body>
<nav class="navbar navbar-inverse navbar-fixed-top">
<div class="container">
<div class="navbar-header">
  <button type="button" class="navbar-toggle collapsed" data-toggle="collapse" data-target="#navbar" aria-expanded="false" aria-controls="navbar">
	<span class="sr-only">Toggle navigation</span>
	<span class="icon-bar"></span>
	<span class="icon-bar"></span>
	<span class="icon-bar"></span>
  </button>
  <a class="navbar-brand" href="#">zdxsv</a>
</div>
</div>
</nav>

<div class="container">
<div class="starter-template">

{{if .Lives}}
{{range .Lives}}
<div class="panel panel-primary">
  <div class="panel-heading">
    <h3 class="panel-title"><span class="glyphicon glyphicon-facetime-video"></span> 生放送中</h3>
  </div>
  <div class="panel-body">
    <div class="media">
      <div class="media-left">
        <img class="media-object" src="{{.ThumbUrl}}" alt="">
      </div>
      <div class="media-body">
        <h4 class="media-heading"><a target="_blank" href="{{.LiveUrl}}">{{.Title}}</a></h4>
        <p>{{.Description}}</p>
		<p>({{.CommunityName}})
		<a target="_blank" href="{{.LiveUrl}}" class="btn btn-primary btn-lg btn-danger pull-right" role="button">視聴する</a></p>
      </div>
    </div>
  </div>
</div>
{{end}}
{{end}}

<h1>SERVER STATUS</h1>
<p> {{.NowDate}} 現在の接続状況  <span class="glyphicon glyphicon-info-sign"></span> 60秒毎に自動更新します</p>
<h3>ロビー {{.LobbyUserCount}} 人</h3>
<table class="table table-inverse table-sm">
<thead>
<tr><th>ID</th><th>HN</th><th>部隊名</th><th>UDP</th></tr>
</thead>
<tbody>
{{range .LobbyUsers}}
<tr><td>【{{.UserId}}】</td><td>{{.Name}}</td><td>{{.Team}}</td><td>{{.UDP}}</td></tr>
{{end}}
</tbody>
</table>
<h3>対戦中 {{.BattleUserCount}} 人</h3>
<table class="table table-inverse table-sm">
<thead>
<tr><th>ID</th><th>HN</th><th>部隊名</th><th>UDP</th></tr>
</thead>
<tbody>
{{range .BattleUsers}}
<tr><td>【{{.UserId}}】</td><td>{{.Name}}</td><td>{{.Team}}</td><td>{{.UDP}}</td></tr>
{{end}}
</tbody>
</table>

<h2>チャット</h2>
{{if .ChatInviteUrl}}
<a target="_blank" href="{{.ChatInviteUrl}}" class="btn btn-primary btn-sm btn-primary" role="button">参加する</a>
{{end}}
{{if .ChatUrl}}
<a target="_blank" href="{{.ChatUrl}}" class="btn btn-primary btn-sm btn-success" role="button">開く</a>
{{end}}

<p>オンライン{{len .OnlineChatUsers}}人 オフライン{{len .OfflineChatUsers}}人</p>
<table class="table table-sm table-striped table-condensed table-inverse">
<tbody>
{{range .OnlineChatUsers}}
<tr>
	<td>
		<span class="glyphicon glyphicon-signal" style="color:green"></span>
		{{if .VoiceChat}}
			<span class="glyphicon glyphicon-volume-up" style="color:green"></span>
		{{else}}
			<span class="glyphicon glyphicon-volume-off" style="color:gray"></span>
		{{end}}
		{{if .Avatar}}
			<img class="img-rounded" width="30" height="30" src="https://cdn.discordapp.com/avatars/{{.ID}}/{{.Avatar}}.jpg">
		{{else}}
			<img class="img-rounded" width="30" height="30" src="/assets/discord.png">
		{{end}}
		{{.Username}}
	</td>
</tr>
{{end}}

{{range .OfflineChatUsers}}
<tr>
	<td>
		<span class="glyphicon glyphicon-signal" style="color:gray"></span>
		<span class="glyphicon glyphicon-volume-off" style="color:gray"></span>
		{{if .Avatar}}
			<img class="img-rounded" width="30" height="30" src="https://cdn.discordapp.com/avatars/{{.ID}}/{{.Avatar}}.jpg">
		{{else}}
			<img class="img-rounded" width="30" height="30" src="/assets/discord.png">
		{{end}}
		{{.Username}}
	</td>
</tr>
{{end}}
</tbody>
</table>


</div>
</div>

<script src="https://ajax.googleapis.com/ajax/libs/jquery/1.12.4/jquery.min.js"></script>
<script src="/assets/js/bootstrap.min.js"></script>

</body>
</html>
