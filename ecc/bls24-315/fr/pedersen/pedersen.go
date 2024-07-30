// Copyright 2020 Consensys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by consensys/gnark-crypto DO NOT EDIT

package pedersen

import (
	"crypto/rand"
	"errors"
	"github.com/consensys/gnark-crypto/ecc"
	curve "github.com/consensys/gnark-crypto/ecc/bls24-315"
	"github.com/consensys/gnark-crypto/ecc/bls24-315/fr"
	"io"
	"math/big"
)

// ProvingKey for committing and proofs of knowledge
type ProvingKey struct {
	Basis         []curve.G1Affine
	BasisExpSigma []curve.G1Affine
}

type VerifyingKey struct {
	G             curve.G2Affine // TODO @tabaie: does this really have to be randomized?
	GRootSigmaNeg curve.G2Affine //gRootSigmaNeg = g^{-1/σ}
}

func randomFrSizedBytes() ([]byte, error) {
	res := make([]byte, fr.Bytes)
	_, err := rand.Read(res)
	return res, err
}

type setupConfig struct {
	g2Gen *curve.G2Affine
}

type SetupOption func(cfg *setupConfig)

func WithG2Point(g2 curve.G2Affine) func(*setupConfig) {
	return func(cfg *setupConfig) {
		cfg.g2Gen = &g2
	}
}

func Setup(bases [][]curve.G1Affine, options ...SetupOption) (pk []ProvingKey, vk VerifyingKey, err error) {
	var cfg setupConfig
	for _, o := range options {
		o(&cfg)
	}
	if cfg.g2Gen == nil {
		if vk.G, err = curve.RandomOnG2(); err != nil {
			return
		}
	} else {
		vk.G = *cfg.g2Gen
	}

	var modMinusOne big.Int
	modMinusOne.Sub(fr.Modulus(), big.NewInt(1))
	var sigma *big.Int
	if sigma, err = rand.Int(rand.Reader, &modMinusOne); err != nil {
		return
	}
	sigma.Add(sigma, big.NewInt(1))

	var sigmaInvNeg big.Int
	sigmaInvNeg.ModInverse(sigma, fr.Modulus())
	sigmaInvNeg.Sub(fr.Modulus(), &sigmaInvNeg)
	vk.GRootSigmaNeg.ScalarMultiplication(&vk.G, &sigmaInvNeg)

	pk = make([]ProvingKey, len(bases))
	for i := range bases {
		pk[i].BasisExpSigma = make([]curve.G1Affine, len(bases[i]))
		for j := range bases[i] {
			pk[i].BasisExpSigma[j].ScalarMultiplication(&bases[i][j], sigma)
		}
		pk[i].Basis = bases[i]
	}
	return
}

func (pk *ProvingKey) ProveKnowledge(values []fr.Element) (pok curve.G1Affine, err error) {
	if len(values) != len(pk.Basis) {
		err = errors.New("must have as many values as basis elements")
		return
	}

	// TODO @gbotrel this will spawn more than one task, see
	// https://github.com/ConsenSys/gnark-crypto/issues/269
	config := ecc.MultiExpConfig{
		NbTasks: 1, // TODO Experiment
	}

	_, err = pok.MultiExp(pk.BasisExpSigma, values, config)
	return
}

func (pk *ProvingKey) Commit(values []fr.Element) (commitment curve.G1Affine, err error) {

	if len(values) != len(pk.Basis) {
		err = errors.New("must have as many values as basis elements")
		return
	}

	// TODO @gbotrel this will spawn more than one task, see
	// https://github.com/ConsenSys/gnark-crypto/issues/269
	config := ecc.MultiExpConfig{
		NbTasks: 1,
	}
	_, err = commitment.MultiExp(pk.Basis, values, config)

	return
}

// BatchProve generates a single proof of knowledge for multiple commitments for faster verification
// The result of this can be verified as a single proof by vk.Verify
func BatchProve(pk []ProvingKey, values [][]fr.Element, challenge fr.Element) (pok curve.G1Affine, err error) {
	if len(pk) != len(values) {
		err = errors.New("must have as many value vectors as bases")
		return
	}

	if len(pk) == 1 { // no need to fold
		pok, err = pk[0].ProveKnowledge(values[0])
		return
	} else if len(pk) == 0 { // nothing to do at all
		return
	}

	offset := 0
	for i := range pk {
		if len(values[i]) != len(pk[i].Basis) {
			err = errors.New("must have as many values as basis elements")
			return
		}
		offset += len(values[i])
	}

	// prepare one amalgamated MSM
	scaledValues := make([]fr.Element, offset)
	basis := make([]curve.G1Affine, offset)

	copy(basis, pk[0].BasisExpSigma)
	copy(scaledValues, values[0])

	offset = len(values[0])
	rI := challenge
	for i := 1; i < len(pk); i++ {
		copy(basis[offset:], pk[i].BasisExpSigma)
		for j := range pk[i].Basis {
			scaledValues[offset].Mul(&values[i][j], &rI)
			offset++
		}
		if i+1 < len(pk) {
			rI.Mul(&rI, &challenge)
		}
	}

	// TODO @gbotrel this will spawn more than one task, see
	// https://github.com/ConsenSys/gnark-crypto/issues/269
	config := ecc.MultiExpConfig{
		NbTasks: 1,
	}

	_, err = pok.MultiExp(basis, scaledValues, config)
	return
}

// FoldCommitments amalgamates multiple commitments into one, which can be verifier against a folded proof obtained from BatchProve
func FoldCommitments(commitments []curve.G1Affine, challenge fr.Element) (commitment curve.G1Affine, err error) {

	if len(commitments) == 1 { // no need to fold
		commitment = commitments[0]
		return
	} else if len(commitments) == 0 { // nothing to do at all
		return
	}

	r := make([]fr.Element, len(commitments))
	r[0].SetOne()
	r[1] = challenge
	for i := 2; i < len(commitments); i++ {
		r[i].Mul(&r[i-1], &r[1])
	}

	for i := range commitments { // TODO @Tabaie Remove if MSM does subgroup check for you
		if !commitments[i].IsInSubGroup() {
			err = errors.New("subgroup check failed")
			return
		}
	}

	// TODO @gbotrel this will spawn more than one task, see
	// https://github.com/ConsenSys/gnark-crypto/issues/269
	config := ecc.MultiExpConfig{
		NbTasks: 1,
	}
	_, err = commitment.MultiExp(commitments, r, config)
	return
}

// Verify checks if the proof of knowledge is valid
func (vk *VerifyingKey) Verify(commitment curve.G1Affine, knowledgeProof curve.G1Affine) error {

	if !commitment.IsInSubGroup() || !knowledgeProof.IsInSubGroup() {
		return errors.New("subgroup check failed")
	}

	if isOne, err := curve.PairingCheck([]curve.G1Affine{commitment, knowledgeProof}, []curve.G2Affine{vk.G, vk.GRootSigmaNeg}); err != nil {
		return err
	} else if !isOne {
		return errors.New("proof rejected")
	}
	return nil
}

// BatchVerify verifies n separately generated proofs of knowledge from different setup ceremonies, using n+1 pairings rather than 2n.
func BatchVerify(vk []VerifyingKey, commitments []curve.G1Affine, pok []curve.G1Affine, challenge fr.Element) error {
	if len(commitments) != len(vk) || len(pok) != len(vk) {
		return errors.New("length mismatch")
	}
	for i := range pok {
		if !pok[i].IsInSubGroup() || !commitments[i].IsInSubGroup() {
			return errors.New("subgroup check failed")
		}
		if i != 0 && vk[i].G != vk[0].G {
			return errors.New("parameter mismatch: G2 element")
		}
	}

	pairingG1 := make([]curve.G1Affine, len(vk)+1)
	pairingG2 := make([]curve.G2Affine, len(vk)+1)
	r := challenge
	pairingG1[0] = pok[0]
	var rI big.Int
	for i := range vk {
		pairingG2[i] = vk[i].GRootSigmaNeg
		if i != 0 {
			r.BigInt(&rI)
			pairingG1[i].ScalarMultiplication(&pok[i], &rI)
			if i+1 != len(vk) {
				r.Mul(&r, &challenge)
			}
		}
	}
	if commitment, err := FoldCommitments(commitments, challenge); err != nil {
		return err
	} else {
		pairingG1[len(vk)] = commitment
	}
	pairingG2[len(vk)] = vk[0].G

	if isOne, err := curve.PairingCheck(pairingG1, pairingG2); err != nil {
		return err
	} else if !isOne {
		return errors.New("proof rejected")
	}
	return nil
}

// Marshal

func (pk *ProvingKey) writeTo(enc *curve.Encoder) (int64, error) {
	if err := enc.Encode(pk.Basis); err != nil {
		return enc.BytesWritten(), err
	}

	err := enc.Encode(pk.BasisExpSigma)

	return enc.BytesWritten(), err
}

func (pk *ProvingKey) WriteTo(w io.Writer) (int64, error) {
	return pk.writeTo(curve.NewEncoder(w))
}

func (pk *ProvingKey) WriteRawTo(w io.Writer) (int64, error) {
	return pk.writeTo(curve.NewEncoder(w, curve.RawEncoding()))
}

func (pk *ProvingKey) ReadFrom(r io.Reader) (int64, error) {
	dec := curve.NewDecoder(r)

	if err := dec.Decode(&pk.Basis); err != nil {
		return dec.BytesRead(), err
	}
	if err := dec.Decode(&pk.BasisExpSigma); err != nil {
		return dec.BytesRead(), err
	}

	if len(pk.Basis) != len(pk.BasisExpSigma) {
		return dec.BytesRead(), errors.New("commitment/proof length mismatch")
	}

	return dec.BytesRead(), nil
}

func (vk *VerifyingKey) WriteTo(w io.Writer) (int64, error) {
	return vk.writeTo(curve.NewEncoder(w))
}

func (vk *VerifyingKey) WriteRawTo(w io.Writer) (int64, error) {
	return vk.writeTo(curve.NewEncoder(w, curve.RawEncoding()))
}

func (vk *VerifyingKey) writeTo(enc *curve.Encoder) (int64, error) {
	var err error

	if err = enc.Encode(&vk.G); err != nil {
		return enc.BytesWritten(), err
	}
	err = enc.Encode(&vk.GRootSigmaNeg)
	return enc.BytesWritten(), err
}

func (vk *VerifyingKey) ReadFrom(r io.Reader) (int64, error) {
	return vk.readFrom(r)
}

func (vk *VerifyingKey) UnsafeReadFrom(r io.Reader) (int64, error) {
	return vk.readFrom(r, curve.NoSubgroupChecks())
}

func (vk *VerifyingKey) readFrom(r io.Reader, decOptions ...func(*curve.Decoder)) (int64, error) {
	dec := curve.NewDecoder(r, decOptions...)
	var err error

	if err = dec.Decode(&vk.G); err != nil {
		return dec.BytesRead(), err
	}
	err = dec.Decode(&vk.GRootSigmaNeg)
	return dec.BytesRead(), err
}
