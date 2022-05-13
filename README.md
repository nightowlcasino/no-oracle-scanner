<div id="top"></div>

<!-- project shields -->
<p align="left">
  <!-- discord -->
  <a href="http://discord.gg/W69GTHe3pJ">
    <img src="https://img.shields.io/static/v1?label=Discord&message=chat&color=5865F2&style=flat&logo=discord"/>
  </a>
  <!-- telegram -->
  <a href="https://t.me/nightowlcommunity">
    <img src="https://img.shields.io/static/v1?label=Telegram&message=chat&color=26A5E4&style=flat&logo=telegram"/>
  </a>
  <!-- reddit -->
  <a href="https://www.reddit.com/r/NightOwlCasino">
    <img src="https://img.shields.io/static/v1?label=Reddit&message=forum&color=FF4500&style=flat&logo=reddit"/>
  </a>
  <!-- mit license -->
  <a href="https://github.com/nightowlcasino/NightOwl-Backend/blob/main/LICENSE">
    <img src="https://img.shields.io/static/v1?label=License&message=MIT&color=A31F34&style=flat"/>
  </a>
</p>

# no-oracle-scanner

## About

> The follow description is subject to change or may not be entirely acurate at the time of this writting.
The no-oracle-scanner periodically calls the Etherscan api and gets the latest block hash as one part of the random number generation for some of the Nightowl Casino games. Once it obtains the newest ETH block hash it then collects all the ERG unconfirmed Txs which will be used in further generation of the Nightowl Casino game random number. Lastly, after a configured amount of time the no-oracle-scanner will then solidify these bets + random numbers onto the ERG blockchain.

## Build

```bash
docker build . -t no-oracle-scanner:<tag>
```

## Start
```bash
docker run -d -it -e ETHERSCAN_API_KEY='ABCDEF' \
-e ERG_NODE_USER='user' \
-e ERG_NODE_PASS='password' \
-e ERG_NODE_API_KEY='abcdef' \
--name no-oracle-scanner no-oracle-scanner:<tag>
```

## License

MIT License, see [LICENSE](https://github.com/nightowlcasino/NightOwl-Backend/blob/main/LICENSE).