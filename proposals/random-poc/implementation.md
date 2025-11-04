Phases:

1. Implement logic in inference-chain, update onlu query interfaces at decentralized api \
Result:
- all unit test ` go test -count=1 ./... ` are passed for both projets
- testermint test passes (will run in CICD but check structures for query is they are used there)
- decentralized api not used


2. Decentralized api is implemented 
- all unit test ` go test -count=1 ./... ` are passed for both projets
- testermint test passes with default value of expected_confirmations_per_epoch=0 (=> ideantical behaviour with now) (will run in CICD but check structures for query is they are used there)


3. Teserming test is implemented 
- new testermint test with expected_confirmations_per_epoch=1 is implemented and passed 